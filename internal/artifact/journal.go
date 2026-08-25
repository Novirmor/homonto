package artifact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// GrantRecord is the durable form of an issued edit grant. It is what
// AcceptEdit trusts: the presented grant only identifies and authenticates
// itself against this record, and every ownership and digest check runs
// against the record's fields. The freshness token is never persisted —
// only its digest is, so a stolen database cannot mint an acceptance.
type GrantRecord struct {
	ID         identity.ActionID
	ActionID   identity.ActionID
	Ref        Ref
	Owner      Owner
	Phase      Phase
	Regions    []Region
	MetaDigest fingerprint.Digest
	Before     fingerprint.Digest
	TokenHash  fingerprint.Digest
	IssuedAt   time.Time
	Consumed   bool
}

// Journal is the durable ledger of edit grants. Grants are single-use:
// Consume must fail when the grant is already consumed, so a replayed
// acceptance can never be applied twice.
type Journal interface {
	// Put records a freshly issued grant. Re-issuing the same id is an
	// error.
	Put(ctx context.Context, rec GrantRecord) error
	// Lookup returns the issued grant, or ok=false when none exists.
	Lookup(ctx context.Context, id identity.ActionID) (GrantRecord, bool, error)
	// Consume marks the grant accepted and records the resulting document
	// digest. Consuming an already-consumed grant returns
	// ErrGrantConsumed.
	Consume(ctx context.Context, id identity.ActionID, at time.Time, result fingerprint.Digest) error
}

// StoreJournal is the SQLite-backed Journal over the runtime database.
type StoreJournal struct {
	db *store.DB
}

// NewStoreJournal binds a grant journal to db.
func NewStoreJournal(db *store.DB) (*StoreJournal, error) {
	if db == nil {
		return nil, fmt.Errorf("artifact: %w", ErrNoJournal)
	}
	return &StoreJournal{db: db}, nil
}

// Put inserts the issued grant. The primary key refuses a duplicate id.
func (j *StoreJournal) Put(ctx context.Context, rec GrantRecord) error {
	regions, err := json.Marshal(rec.Regions)
	if err != nil {
		return fmt.Errorf("artifact: encode grant regions: %w", err)
	}
	var actionID any
	if rec.ActionID != "" {
		actionID = string(rec.ActionID)
	}
	return j.db.Update(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO artifact_grants
			  (id, action_id, work_id, kind, path, owner, phase, regions,
			   meta_digest, before_digest, token_hash, issued_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(rec.ID), actionID, string(rec.Ref.WorkID), string(rec.Ref.Kind),
			rec.Ref.Path, string(rec.Owner), string(rec.Phase), string(regions),
			string(rec.MetaDigest), string(rec.Before), string(rec.TokenHash),
			rec.IssuedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("artifact: record grant %s: %w", rec.ID, err)
		}
		return nil
	})
}

// Lookup reads the issued grant back.
func (j *StoreJournal) Lookup(ctx context.Context, id identity.ActionID) (GrantRecord, bool, error) {
	var (
		rec      GrantRecord
		actionID sql.NullString
		regions  string
		issuedAt string
		consumed sql.NullString
	)
	err := j.db.View(ctx, func(tx *store.Tx) error {
		row := tx.QueryRowContext(ctx, `
			SELECT id, action_id, work_id, kind, path, owner, phase, regions,
			       meta_digest, before_digest, token_hash, issued_at, consumed_at
			  FROM artifact_grants WHERE id = ?`, string(id))
		return row.Scan(&rec.ID, &actionID, &rec.Ref.WorkID, &rec.Ref.Kind, &rec.Ref.Path,
			&rec.Owner, &rec.Phase, &regions, &rec.MetaDigest, &rec.Before,
			&rec.TokenHash, &issuedAt, &consumed)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return GrantRecord{}, false, nil
	}
	if err != nil {
		return GrantRecord{}, false, fmt.Errorf("artifact: read grant %s: %w", id, err)
	}
	if actionID.Valid {
		rec.ActionID = identity.ActionID(actionID.String)
	}
	if err := json.Unmarshal([]byte(regions), &rec.Regions); err != nil {
		return GrantRecord{}, false, fmt.Errorf("artifact: decode grant %s regions: %w", id, err)
	}
	rec.IssuedAt, err = time.Parse(time.RFC3339Nano, issuedAt)
	if err != nil {
		return GrantRecord{}, false, fmt.Errorf("artifact: decode grant %s issue time: %w", id, err)
	}
	rec.Consumed = consumed.Valid
	return rec, true, nil
}

// Consume marks the grant accepted. The conditional UPDATE is the
// single-use gate: it matches only a row that is still unconsumed, so two
// concurrent acceptances cannot both win.
func (j *StoreJournal) Consume(ctx context.Context, id identity.ActionID, at time.Time, result fingerprint.Digest) error {
	return j.db.Update(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE artifact_grants SET consumed_at = ?, result_digest = ?
			 WHERE id = ? AND consumed_at IS NULL`,
			at.UTC().Format(time.RFC3339Nano), string(result), string(id))
		if err != nil {
			return fmt.Errorf("artifact: consume grant %s: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("artifact: consume grant %s: %w", id, err)
		}
		if n == 0 {
			return fmt.Errorf("artifact: grant %s: %w", id, ErrGrantConsumed)
		}
		return nil
	})
}
