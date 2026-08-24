package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Tx is one database transaction, handed to View and Update callbacks. It
// deliberately exposes only statement methods — a callback must not commit
// or roll back its own envelope. Callbacks must not use the owning DB while
// the transaction is open: the pool holds a single connection, so nesting
// would self-deadlock.
type Tx struct {
	tx *sql.Tx
}

// ExecContext executes a statement that returns no rows.
func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.tx.ExecContext(ctx, query, args...)
}

// QueryContext executes a query that returns rows.
func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.tx.QueryContext(ctx, query, args...)
}

// QueryRowContext executes a query expected to return at most one row.
func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

// View runs fn in a transaction intended for reads. With a read-write open
// the transaction still begins immediately (the connection's configured
// BEGIN mode), which serializes readers against the single writer — the
// runtime's intended concurrency contract.
func (db *DB) View(ctx context.Context, fn func(*Tx) error) error {
	return db.withTx(ctx, fn)
}

// Update runs fn in a write transaction: either every statement commits, or
// an error from fn (or a failed statement) rolls the whole transaction back.
func (db *DB) Update(ctx context.Context, fn func(*Tx) error) error {
	return db.withTx(ctx, fn)
}

// withTx runs fn inside one transaction, rolling back on error and
// committing only when fn succeeds.
func (db *DB) withTx(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction on %s: %w", db.path, err)
	}
	if err := fn(&Tx{tx: sqlTx}); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			return fmt.Errorf("store: rollback after error %v: %w", err, rbErr)
		}
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction on %s: %w", db.path, err)
	}
	return nil
}
