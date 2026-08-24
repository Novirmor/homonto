package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
