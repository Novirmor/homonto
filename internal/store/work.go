package store

import (
	"context"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

// WorkRecord is one row of the works table as written by the runtime. The
// state machine (which states exist and how they transition) belongs to the
// workflow engines; this store layer only persists rows.
type WorkRecord struct {
	ID        identity.WorkID
	Kind      string
	Title     string
	State     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertWork inserts the work row or, when the work already exists, replaces
// only its state and updated_at — the kind and title of a row are set once
// at first insertion and preserved by later activations.
func (tx *Tx) UpsertWork(ctx context.Context, rec WorkRecord) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO works (id, kind, title, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET state=excluded.state, updated_at=excluded.updated_at`,
		string(rec.ID), rec.Kind, rec.Title, rec.State,
		formatTime(rec.CreatedAt), formatTime(rec.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: upsert work %s: %w", rec.ID, err)
	}
	return nil
}

// SetMeta inserts or replaces one machine key/value metadata entry.
func (tx *Tx) SetMeta(ctx context.Context, key, value string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("store: set meta %s: %w", key, err)
	}
	return nil
}
