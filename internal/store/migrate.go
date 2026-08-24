package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/schema"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SchemaTooNewError reports a database whose recorded schema version was
// written by a newer binary. It wraps schema.ErrTooNew so callers can detect
// "binary too old" with errors.Is at any distance.
type SchemaTooNewError struct {
	Path      string
	Have      int64
	Supported int64
}

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf(
		"store: %s: database schema version %d is newer than this binary supports (up to %d) — upgrade homonto",
		e.Path, e.Have, e.Supported)
}

func (e *SchemaTooNewError) Unwrap() error { return schema.ErrTooNew }

// migration is one versioned schema change embedded at build time.
type migration struct {
	version int64
	name    string
	sql     string
}

// migrationNameFormat is the required file name: a zero-padded version
// prefix, an underscore, a descriptive stem, and .sql.
const migrationSep = "_"

// embeddedMigrations returns the embedded migrations sorted by version.
func embeddedMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read embedded migrations: %w", err)
	}
	var ms []migration
	seen := map[int64]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), migrationSep)
		if !ok {
			return nil, fmt.Errorf("store: migration file %s: missing %q separator", entry.Name(), migrationSep)
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("store: migration file %s: version prefix %q is not a positive integer", entry.Name(), prefix)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("store: migration version %d defined twice: %s and %s", version, prev, entry.Name())
		}
		seen[version] = entry.Name()
		body, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read embedded migration %s: %w", entry.Name(), err)
		}
		ms = append(ms, migration{version: version, name: entry.Name(), sql: string(body)})
	}
	if len(ms) == 0 {
		return nil, errors.New("store: no embedded migrations")
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// SchemaVersion returns the highest migration version this binary knows.
func SchemaVersion() int64 {
	ms, err := embeddedMigrations()
	if err != nil {
		panic(err)
	}
	return ms[len(ms)-1].version
}

// migrate brings the database up to the latest embedded schema version. It
// creates the migrations ledger, refuses databases recorded at a newer
// version than this binary supports, and applies each pending migration
// together with its ledger row in one transaction.
func (db *DB) migrate(ctx context.Context) error {
	ms, err := embeddedMigrations()
	if err != nil {
		return err
	}
	latest := ms[len(ms)-1].version

	if _, err := db.conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: %s: create migrations table: %w", db.path, err)
	}

	applied, maxApplied, err := db.readMigrations(ctx)
	if err != nil {
		return err
	}
	if maxApplied > latest {
		return &SchemaTooNewError{Path: db.path, Have: maxApplied, Supported: latest}
	}

	for _, m := range ms {
		if _, ok := applied[m.version]; ok {
			continue
		}
		err := db.withTx(ctx, func(tx *Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("apply %s: %w", m.name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.version, m.name, formatTime(time.Now()))
			return err
		})
		if err != nil {
			return fmt.Errorf("store: %s: %w", db.path, err)
		}
	}
	return nil
}

// checkReadOnly validates a read-only database without writing to it: the
// schema must already be migrated, and not at a version newer than this
// binary supports.
func (db *DB) checkReadOnly(ctx context.Context) error {
	var n int
	if err := db.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migrations'`).Scan(&n); err != nil {
		return fmt.Errorf("store: %s: inspect schema: %w", db.path, err)
	}
	if n == 0 {
		return fmt.Errorf(
			"store: %s: database is not migrated — open it read-write once to initialize (read-only open never migrates or creates)",
			db.path)
	}
	_, maxApplied, err := db.readMigrations(ctx)
	if err != nil {
		return err
	}
	// A migrations table with no rows is an initialized-then-emptied (or
	// hand-planted) database: nothing was ever applied, so it is unmigrated
	// and must fail here, not at the first query of a missing table.
	if maxApplied < 1 {
		return fmt.Errorf(
			"store: %s: database is not migrated — open it read-write once to initialize (read-only open never migrates or creates)",
			db.path)
	}
	if maxApplied > SchemaVersion() {
		return &SchemaTooNewError{Path: db.path, Have: maxApplied, Supported: SchemaVersion()}
	}
	return nil
}

// readMigrations returns the applied migration ledger keyed by version plus
// the highest applied version (0 when none).
func (db *DB) readMigrations(ctx context.Context) (map[int64]string, int64, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT version, name FROM migrations`)
	if err != nil {
		return nil, 0, fmt.Errorf("store: %s: read migrations: %w", db.path, err)
	}
	defer rows.Close()
	applied := map[int64]string{}
	var maxVersion int64
	for rows.Next() {
		var version int64
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, 0, fmt.Errorf("store: %s: scan migration row: %w", db.path, err)
		}
		applied[version] = name
		if version > maxVersion {
			maxVersion = version
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: %s: read migrations: %w", db.path, err)
	}
	return applied, maxVersion, nil
}
