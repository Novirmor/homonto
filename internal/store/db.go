// Package store owns the SQLite runtime database: opening (read-write with
// migrations and durability pragmas, read-only without ever creating or
// migrating), schema versioning, transactional View/Update primitives, and
// the persisted operation/effect journal tables every workflow package
// builds on.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrUnrecoveredWAL is the cause attached to a failed read-only open of a
// database whose WAL still holds committed frames — exactly what a crashed
// process leaves behind. SQLite cannot run WAL recovery through a read-only
// connection (it must build the wal-index), so the raw failure is an opaque
// "unable to open database file"; this sentinel names the remedy: one
// read-write open performs the recovery pass.
//
// The underlying SQLite error remains wrapped for errors.Is/As.
var ErrUnrecoveredWAL = errors.New("read-only open of a database with an unrecovered WAL requires a read-write recovery pass first")

// OpenOptions selects how Open treats the database file.
type OpenOptions struct {
	// ReadOnly opens the database with SQLite mode=ro: a missing file is an
	// error and is never created, migrations never run, and no write pragmas
	// are applied. The database must already have been migrated by a
	// read-write open.
	ReadOnly bool
}

// DB is a configured connection pool for one runtime database. It is safe
// for concurrent use; the pool holds a single connection (MaxOpenConns(1))
// and every transaction begins immediately (BEGIN IMMEDIATE on read-write
// opens), so all access serializes through one writer instead of failing
// with SQLITE_BUSY at upgrade time.
type DB struct {
	conn     *sql.DB
	path     string
	readOnly bool
}

// Open connects to the SQLite database at path.
//
// A read-write open creates the file when absent (or migrates an empty one),
// applies every pending embedded migration in order, and configures each
// connection with foreign_keys=ON, journal_mode=WAL, synchronous=FULL,
// busy_timeout=5000ms, and BEGIN IMMEDIATE transactions.
//
// A read-only open passes mode=ro to SQLite — a missing file is an error,
// never a creation — and runs no migrations and no write pragmas.
//
// Both modes refuse a database whose recorded schema version is newer than
// this binary supports; the returned error wraps schema.ErrTooNew.
func Open(ctx context.Context, path string, opts OpenOptions) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %s: %w", path, err)
	}
	dsn := url.URL{Scheme: "file", Path: abs}
	query := dsn.Query()
	if opts.ReadOnly {
		query.Set("mode", "ro")
	} else {
		query.Add("_pragma", "busy_timeout(5000)")
		query.Add("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "journal_mode(WAL)")
		query.Add("_pragma", "synchronous(FULL)")
		query.Set("_txlock", "immediate")
	}
	dsn.RawQuery = query.Encode()

	conn, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// One connection serializes all database access; anything wider would
	// let two goroutines hold conflicting views of the journal.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)
	db := &DB{conn: conn, path: abs, readOnly: opts.ReadOnly}

	// database/sql connects lazily; ping so open failures (a missing file
	// under mode=ro, a non-database file, permission errors) surface here.
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		err = fmt.Errorf("store: connect %s: %w", path, err)
		if opts.ReadOnly {
			err = withWALHint(path, abs, err)
		}
		return nil, err
	}

	if opts.ReadOnly {
		if err := db.checkReadOnly(ctx); err != nil {
			_ = conn.Close()
			return nil, withWALHint(path, abs, err)
		}
		return db, nil
	}
	if err := db.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the database handle.
func (db *DB) Close() error {
	if err := db.conn.Close(); err != nil {
		return fmt.Errorf("store: close %s: %w", db.path, err)
	}
	return nil
}

// Path returns the absolute path the database was opened from.
func (db *DB) Path() string { return db.path }

// ReadOnly reports whether the database was opened read-only.
func (db *DB) ReadOnly() bool { return db.readOnly }

// withWALHint decorates a failed read-only open with the unrecovered-WAL
// cause when a non-empty WAL file sits beside the database. Deliberately not
// immutable=1: lying about the file's mutability to sneak a read through
// would report a stale main database and silently drop the committed WAL
// frames — the hint and a read-write recovery pass are the honest fix.
func withWALHint(path, abs string, err error) error {
	if st, statErr := os.Stat(abs + "-wal"); statErr == nil && st.Size() > 0 {
		return fmt.Errorf("store: %s: %w: %w", path, ErrUnrecoveredWAL, err)
	}
	return err
}
