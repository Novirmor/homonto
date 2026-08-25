package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/schema"
)

// openRW opens a read-write test database at path and closes it on cleanup.
func openRW(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(context.Background(), path, OpenOptions{})
	if err != nil {
		t.Fatalf("store: open read-write %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// tableExists reports whether name is a table in db's schema.
func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	ctx := context.Background()
	var one int
	err := db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&one)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("store: inspect sqlite_master for %s: %v", name, err)
	}
	return true
}

func TestOpenAppliesMigrationsToNewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db := openRW(t, path)

	if !tableExists(t, db, "migrations") {
		t.Fatal("store: migrations table missing after read-write open")
	}
	for _, name := range []string{
		"meta", "works", "members", "operations", "operation_effects",
		"actions", "action_dependencies", "reports", "checks", "findings",
		"decisions", "facts", "fact_edges", "leases", "update_journal",
	} {
		if !tableExists(t, db, name) {
			t.Errorf("store: table %s missing after migration", name)
		}
	}

	ctx := context.Background()
	var version int64
	err := db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT MAX(version) FROM migrations`).Scan(&version)
	})
	if err != nil {
		t.Fatalf("store: read applied version: %v", err)
	}
	if version != SchemaVersion() {
		t.Errorf("store: applied schema version = %d, want %d", version, SchemaVersion())
	}
}

func TestOpenMigratesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("store: create empty file: %v", err)
	}
	db := openRW(t, path)
	if !tableExists(t, db, "operations") {
		t.Error("store: empty file was not migrated by read-write open")
	}
}

func TestOpenReadOnlyNonexistentPathErrorsWithoutCreating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sqlite")
	_, err := Open(context.Background(), path, OpenOptions{ReadOnly: true})
	if err == nil {
		t.Fatal("store: read-only open of nonexistent path succeeded, want error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("store: read-only open created the file: stat err = %v", statErr)
	}
}

func TestOpenReadOnlyUnmigratedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("store: create empty file: %v", err)
	}
	db, err := Open(context.Background(), path, OpenOptions{ReadOnly: true})
	if err == nil {
		t.Cleanup(func() { _ = db.Close() })
		t.Fatal("store: read-only open of unmigrated database succeeded, want error")
	}
}

func TestOpenSchemaTooNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db := openRW(t, path)
	ctx := context.Background()
	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO migrations (version, name, applied_at) VALUES (999, '999_future.sql', '2026-01-01T00:00:00Z')`)
		return err
	})
	if err != nil {
		t.Fatalf("store: plant future version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("store: close: %v", err)
	}

	t.Run("read-write", func(t *testing.T) {
		_, err := Open(ctx, path, OpenOptions{})
		if !errors.Is(err, schema.ErrTooNew) {
			t.Errorf("store: read-write open of too-new database: errors.Is(schema.ErrTooNew) = false, err = %v", err)
		}
	})
	t.Run("read-only", func(t *testing.T) {
		_, err := Open(ctx, path, OpenOptions{ReadOnly: true})
		if !errors.Is(err, schema.ErrTooNew) {
			t.Errorf("store: read-only open of too-new database: errors.Is(schema.ErrTooNew) = false, err = %v", err)
		}
	})
}

func TestOpenReadWritePragmas(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()

	var journalMode string
	var synchronous, foreignKeys, busyTimeout int
	err := db.View(ctx, func(tx *Tx) error {
		if err := tx.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout)
	})
	if err != nil {
		t.Fatalf("store: read pragmas: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("store: journal_mode = %q, want wal", journalMode)
	}
	if synchronous != 2 {
		t.Errorf("store: synchronous = %d, want 2 (FULL)", synchronous)
	}
	if foreignKeys != 1 {
		t.Errorf("store: foreign_keys = %d, want 1", foreignKeys)
	}
	if busyTimeout != 5000 {
		t.Errorf("store: busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestUpdateCommitsAndRollsBack(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
		return err
	})
	if err != nil {
		t.Fatalf("store: update: %v", err)
	}

	boom := errors.New("boom")
	err = db.Update(ctx, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('gone', 'x')`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("store: update returned %v, want injected error", err)
	}

	var value string
	err = db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='k'`).Scan(&value)
	})
	if err != nil || value != "v" {
		t.Errorf("store: committed row missing: value=%q err=%v", value, err)
	}

	err = db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='gone'`).Scan(&value)
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("store: rolled-back row visible: err = %v, want no rows", err)
	}
}

func TestConcurrentUpdatesSerialize(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('counter', '0')`)
		return err
	})
	if err != nil {
		t.Fatalf("store: seed counter: %v", err)
	}

	const goroutines, increments = 8, 5
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				err := db.Update(ctx, func(tx *Tx) error {
					_, err := tx.ExecContext(ctx,
						`UPDATE meta SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT) WHERE key='counter'`)
					return err
				})
				if err != nil {
					t.Errorf("store: concurrent update: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	var counter string
	err = db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='counter'`).Scan(&counter)
	})
	if err != nil {
		t.Fatalf("store: read counter: %v", err)
	}
	if counter != "40" {
		t.Errorf("store: counter = %s, want 40 (lost updates under MaxOpenConns(1))", counter)
	}
}

func TestOpenReadOnlyUnrecoveredWALHintsAtRecoveryPass(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.sqlite")
	crashDir := filepath.Join(dir, "crash")
	crashed := filepath.Join(crashDir, "crashed.sqlite")
	ctx := context.Background()

	db, err := Open(ctx, live, OpenOptions{})
	if err != nil {
		t.Fatalf("store: open live: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The committed row lives in the hot WAL: the connection stays open, so
	// no clean shutdown checkpoints it away.
	err = db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`)
		return err
	})
	if err != nil {
		t.Fatalf("store: seed live row: %v", err)
	}

	// Plant the crash debris — db + hot WAL, no -shm — in a directory the
	// reading process cannot write. That is the post-crash configuration
	// where a read-only open genuinely fails: SQLite cannot build the
	// wal-index without creating the -shm, and reports only an opaque
	// "unable to open database file".
	main, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("store: read live database: %v", err)
	}
	wal, err := os.ReadFile(live + "-wal")
	if err != nil {
		t.Fatalf("store: read live WAL: %v (test premise: WAL is hot while the writer is open)", err)
	}
	if len(wal) == 0 {
		t.Fatal("store: live WAL is empty — test premise broken")
	}
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatalf("store: create crash dir: %v", err)
	}
	if err := os.WriteFile(crashed, main, 0o600); err != nil {
		t.Fatalf("store: plant crashed database: %v", err)
	}
	if err := os.WriteFile(crashed+"-wal", wal, 0o600); err != nil {
		t.Fatalf("store: plant crashed WAL: %v", err)
	}
	// Restore writability no matter where the test ends: t.TempDir's
	// cleanup must be able to remove the directory.
	writable := func() { _ = os.Chmod(crashDir, 0o700) }
	t.Cleanup(writable)
	if err := os.Chmod(crashDir, 0o500); err != nil {
		t.Fatalf("store: make crash dir read-only: %v", err)
	}

	_, err = Open(ctx, crashed, OpenOptions{ReadOnly: true})
	if !errors.Is(err, ErrUnrecoveredWAL) {
		t.Errorf("store: read-only open of unrecovered WAL: errors.Is(ErrUnrecoveredWAL) = false, err = %v", err)
	}

	// The remedy the hint names: one read-write pass recovers the WAL.
	writable()
	db2, err := Open(ctx, crashed, OpenOptions{})
	if err != nil {
		t.Fatalf("store: read-write recovery open: %v", err)
	}
	defer func() { _ = db2.Close() }()
	var value string
	err = db2.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='k'`).Scan(&value)
	})
	if err != nil || value != "v" {
		t.Errorf("store: WAL-lost row after recovery pass: value=%q err=%v", value, err)
	}
}

func TestOpenReadOnlyEmptyMigrationsLedgerErrorsAtOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db := openRW(t, path)
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM migrations`)
		return err
	})
	if err != nil {
		t.Fatalf("store: empty migrations ledger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("store: close: %v", err)
	}

	_, err = Open(ctx, path, OpenOptions{ReadOnly: true})
	if err == nil {
		t.Error("store: read-only open of a database with an empty migrations ledger succeeded, want error at open")
	}
}

func TestOpenReadOnlyReadsRowsAndRejectsWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db := openRW(t, path)
	ctx := context.Background()

	err := db.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('ro', 'ok')`)
		return err
	})
	if err != nil {
		t.Fatalf("store: seed row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("store: close read-write handle: %v", err)
	}

	ro, err := Open(ctx, path, OpenOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("store: read-only open of migrated database: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	var value string
	err = ro.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='ro'`).Scan(&value)
	})
	if err != nil || value != "ok" {
		t.Errorf("store: read-only read: value=%q err=%v, want ok", value, err)
	}

	err = ro.Update(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('write', 'attempt')`)
		return err
	})
	if err == nil {
		t.Error("store: write through a read-only open succeeded, want error")
	}
}

// TestMigratingAPopulatedOlderDatabaseKeepsItsRows is the migration
// rehearsal in test form.
//
// Every other migration test starts from an empty file, which proves the
// SQL parses and nothing more. What an update actually does is run these
// migrations over a database with a user's work in it, and the failure
// that matters is a migration that succeeds while losing rows — an update
// that installs cleanly and silently discards the workflow it was meant
// to carry forward.
//
// The test plants a database at the schema BEFORE the current one, with a
// row in each table the newest migration touches, then opens it normally
// and looks for the rows.
func TestMigratingAPopulatedOlderDatabaseKeepsItsRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")

	previous := SchemaVersion() - 1
	if previous < 1 {
		t.Skip("there is no earlier schema to migrate from")
	}
	if err := migrateTo(ctx, path, previous); err != nil {
		t.Fatalf("plant a database at schema %d: %v", previous, err)
	}

	// One work and one unit record, written through the OLD schema's
	// table name. The newest migration renames it.
	const (
		workID   = "11111111-1111-4111-8111-111111111111"
		actionID = "22222222-2222-4222-8222-222222222222"
	)
	if err := execOn(ctx, path, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO works (id, kind, title, state, created_at, updated_at)
			VALUES (?, 'task', 'carry me forward', 'active', ?, ?)`,
			workID, formatTime(time.Now()), formatTime(time.Now())); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `
			INSERT INTO task_partitions (action_id, work_id, step, partition)
			VALUES (?, ?, 'do_implement', '{"Label":"item-1"}')`, actionID, workID)
		return err
	}); err != nil {
		t.Fatalf("populate the old database: %v", err)
	}

	// The update: open it with this binary, which migrates it forward.
	db, err := Open(ctx, path, OpenOptions{})
	if err != nil {
		t.Fatalf("migrate the populated database: %v", err)
	}
	defer db.Close()

	var (
		title string
		label string
	)
	if err := db.View(ctx, func(tx *Tx) error {
		if err := tx.QueryRowContext(ctx,
			`SELECT title FROM works WHERE id = ?`, workID).Scan(&title); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx,
			`SELECT partition FROM work_units WHERE action_id = ?`, actionID).Scan(&label)
	}); err != nil {
		t.Fatalf("the migration lost rows: %v", err)
	}
	if title != "carry me forward" {
		t.Errorf("work title = %q after migrating", title)
	}
	if !strings.Contains(label, "item-1") {
		t.Errorf("unit record = %q after the rename", label)
	}
}

// migrateTo plants a database at an exact schema version by applying the
// embedded migrations up to it and recording them in the ledger — the same
// way migrate does, stopping early.
func migrateTo(ctx context.Context, path string, version int64) error {
	ms, err := embeddedMigrations()
	if err != nil {
		return err
	}
	return execOn(ctx, path, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
			return err
		}
		for _, m := range ms {
			if m.version > version {
				break
			}
			if _, err := db.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("apply %s: %w", m.name, err)
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.version, m.name, formatTime(time.Now())); err != nil {
				return err
			}
		}
		return nil
	})
}

// execOn opens the database file directly, outside Open, so the test can
// write to a schema this binary would otherwise migrate on sight.
func execOn(ctx context.Context, path string, fn func(*sql.DB) error) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}
