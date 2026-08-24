package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Operation lifecycle states. Every transition is committed immediately by
// the operation package; the committed sequence is the crash journal.
const (
	// OpPending: the intent row is committed but Prepare has not.
	OpPending = "pending"
	// OpPrepared: effects are journalled with their payloads; Apply may be
	// partway through.
	OpPrepared = "prepared"
	// OpFinalized: every effect is applied and the operation is complete.
	OpFinalized = "finalized"
	// OpRolledBack: applied effects were reverted (or nothing was applied)
	// and the operation is closed without completing.
	OpRolledBack = "rolled_back"
)

// Effect lifecycle states within one operation.
const (
	EffectPending  = "pending"
	EffectApplied  = "applied"
	EffectReverted = "reverted"
	// EffectFailed: Apply returned an error. Terminal for the operation —
	// recovery never re-applies the row (and never reverts it); the
	// operation recovers under roll-back.
	EffectFailed = "failed"
)

// OperationRecord is one row of the operations journal.
type OperationRecord struct {
	ID         identity.OperationID
	Kind       string
	State      string
	WorkID     identity.WorkID // empty persists as NULL
	Generation int64
	Policy     string
	Payload    json.RawMessage
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// EffectRow is one row of the operation_effects journal.
type EffectRow struct {
	OpID    identity.OperationID
	Seq     int64
	Kind    string
	State   string
	Payload json.RawMessage
}

// InsertOperation commits the operation's intent row.
func (tx *Tx) InsertOperation(ctx context.Context, rec OperationRecord) error {
	workID := sql.NullString{String: string(rec.WorkID), Valid: rec.WorkID != ""}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO operations (id, kind, state, work_id, generation, policy, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(rec.ID), rec.Kind, rec.State, workID, rec.Generation, rec.Policy,
		string(rec.Payload), formatTime(rec.CreatedAt), formatTime(rec.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: insert operation %s: %w", rec.ID, err)
	}
	return nil
}

// SetOperationState commits a state transition for the operation and
// advances updated_at. It fails when the operation row does not exist.
func (tx *Tx) SetOperationState(ctx context.Context, id identity.OperationID, state string) error {
	res, err := tx.ExecContext(ctx, `UPDATE operations SET state=?, updated_at=? WHERE id=?`,
		state, formatTime(time.Now()), string(id))
	if err != nil {
		return fmt.Errorf("store: update operation %s to %s: %w", id, state, err)
	}
	return requireUpdated(res, fmt.Sprintf("operation %s", id))
}

// SetOperationPolicy replaces the persisted crash-recovery policy of an
// operation. Recovery uses it to switch a prepared operation to roll-back
// when rolling forward is impossible; the transition is its own committed
// step so an interrupted recovery pass re-decides on the next pass.
func (tx *Tx) SetOperationPolicy(ctx context.Context, id identity.OperationID, policy string) error {
	res, err := tx.ExecContext(ctx, `UPDATE operations SET policy=?, updated_at=? WHERE id=?`,
		policy, formatTime(time.Now()), string(id))
	if err != nil {
		return fmt.Errorf("store: update operation %s policy to %s: %w", id, policy, err)
	}
	return requireUpdated(res, fmt.Sprintf("operation %s", id))
}

// InsertEffect commits one journalled effect of an operation.
func (tx *Tx) InsertEffect(ctx context.Context, row EffectRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO operation_effects (op_id, seq, kind, state, payload)
		VALUES (?, ?, ?, ?, ?)`,
		string(row.OpID), row.Seq, row.Kind, row.State, string(row.Payload))
	if err != nil {
		return fmt.Errorf("store: insert effect %d of %s: %w", row.Seq, row.OpID, err)
	}
	return nil
}

// SetEffectState commits an effect state transition. It fails when the
// (operation, seq) row does not exist.
func (tx *Tx) SetEffectState(ctx context.Context, opID identity.OperationID, seq int64, state string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE operation_effects SET state=? WHERE op_id=? AND seq=?`,
		state, string(opID), seq)
	if err != nil {
		return fmt.Errorf("store: update effect %d of %s to %s: %w", seq, opID, state, err)
	}
	return requireUpdated(res, fmt.Sprintf("effect %d of %s", seq, opID))
}

// PendingOperations returns every operation not yet in a terminal state
// (pending or prepared), oldest first, deterministically ordered.
func (db *DB) PendingOperations(ctx context.Context) ([]OperationRecord, error) {
	var recs []OperationRecord
	err := db.View(ctx, func(tx *Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, kind, state, work_id, generation, policy, payload, created_at, updated_at
			FROM operations
			WHERE state IN (?, ?)
			ORDER BY created_at, id`, OpPending, OpPrepared)
		if err != nil {
			return fmt.Errorf("store: query pending operations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			rec, err := scanOperation(rows)
			if err != nil {
				return err
			}
			recs = append(recs, rec)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("store: list pending operations on %s: %w", db.path, err)
	}
	return recs, nil
}

// Operation returns one operation row by id, in every lifecycle state. It
// fails when no such row exists, wrapping ErrNoJournalRow.
func (db *DB) Operation(ctx context.Context, id identity.OperationID) (OperationRecord, error) {
	var rec OperationRecord
	err := db.View(ctx, func(tx *Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT id, kind, state, work_id, generation, policy, payload, created_at, updated_at
			FROM operations WHERE id=?`, string(id))
		if err := scanOperationRow(row, &rec); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OperationRecord{}, fmt.Errorf("store: operation %s: %w", id, ErrNoJournalRow)
		}
		return OperationRecord{}, fmt.Errorf("store: load operation %s: %w", id, err)
	}
	return rec, nil
}

// scanOperationRow scans one operations row into rec.
func scanOperationRow(row *sql.Row, rec *OperationRecord) error {
	var id, payload, createdAt, updatedAt string
	var workID sql.NullString
	if err := row.Scan(&id, &rec.Kind, &rec.State, &workID, &rec.Generation,
		&rec.Policy, &payload, &createdAt, &updatedAt); err != nil {
		return err
	}
	rec.ID = identity.OperationID(id)
	rec.WorkID = identity.WorkID(workID.String)
	rec.Payload = json.RawMessage(payload)
	var err error
	if rec.CreatedAt, err = parseTime(createdAt); err != nil {
		return fmt.Errorf("store: scan created_at of operation %s: %w", id, err)
	}
	if rec.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return fmt.Errorf("store: scan updated_at of operation %s: %w", id, err)
	}
	return nil
}

// OperationEffects returns the effect journal of one operation in apply
// order.
func (db *DB) OperationEffects(ctx context.Context, opID identity.OperationID) ([]EffectRow, error) {
	var rowsOut []EffectRow
	err := db.View(ctx, func(tx *Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT op_id, seq, kind, state, payload
			FROM operation_effects
			WHERE op_id = ?
			ORDER BY seq`, string(opID))
		if err != nil {
			return fmt.Errorf("store: query effects of %s: %w", opID, err)
		}
		defer rows.Close()
		for rows.Next() {
			var row EffectRow
			var opIDStr, payload string
			if err := rows.Scan(&opIDStr, &row.Seq, &row.Kind, &row.State, &payload); err != nil {
				return fmt.Errorf("store: scan effect of %s: %w", opID, err)
			}
			row.OpID = identity.OperationID(opIDStr)
			row.Payload = json.RawMessage(payload)
			rowsOut = append(rowsOut, row)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("store: list effects of %s on %s: %w", opID, db.path, err)
	}
	return rowsOut, nil
}

func scanOperation(rows *sql.Rows) (OperationRecord, error) {
	var rec OperationRecord
	var id, payload, createdAt, updatedAt string
	var workID sql.NullString
	if err := rows.Scan(&id, &rec.Kind, &rec.State, &workID, &rec.Generation,
		&rec.Policy, &payload, &createdAt, &updatedAt); err != nil {
		return OperationRecord{}, fmt.Errorf("store: scan operation row: %w", err)
	}
	rec.ID = identity.OperationID(id)
	rec.WorkID = identity.WorkID(workID.String)
	rec.Payload = json.RawMessage(payload)
	var err error
	if rec.CreatedAt, err = parseTime(createdAt); err != nil {
		return OperationRecord{}, fmt.Errorf("store: scan created_at of operation %s: %w", id, err)
	}
	if rec.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return OperationRecord{}, fmt.Errorf("store: scan updated_at of operation %s: %w", id, err)
	}
	return rec, nil
}

// ErrNoJournalRow is the sentinel requireUpdated wraps: an UPDATE matched no
// row, so the named journal entity does not exist.
var ErrNoJournalRow = errors.New("store: no such journal row")

// requireUpdated fails when an UPDATE matched no row, so journal transitions
// cannot silently no-op on unknown ids.
func requireUpdated(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update %s: rows affected: %w", what, err)
	}
	if n != 1 {
		return fmt.Errorf("store: update %s: %w", what, ErrNoJournalRow)
	}
	return nil
}

// formatTime renders t as UTC RFC 3339 with nanosecond precision.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime decodes what formatTime wrote; the empty string is the zero time.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse timestamp %q: %w", s, err)
	}
	return t, nil
}
