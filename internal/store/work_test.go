package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

func TestUpsertWorkActivatesAndPreservesIdentity(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("store: work id: %v", err)
	}
	now := time.Now().UTC()

	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.UpsertWork(ctx, WorkRecord{
			ID: workID, Kind: "task", Title: "First", State: "active",
			CreatedAt: now, UpdatedAt: now,
		})
	}); err != nil {
		t.Fatalf("store: upsert work: %v", err)
	}

	var kind, title, state string
	err = db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT kind, title, state FROM works WHERE id=?`, string(workID)).Scan(&kind, &title, &state)
	})
	if err != nil {
		t.Fatalf("store: read work: %v", err)
	}
	if kind != "task" || title != "First" || state != "active" {
		t.Errorf("store: work row = %s/%s/%s, want task/First/active", kind, title, state)
	}

	later := now.Add(time.Minute)
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.UpsertWork(ctx, WorkRecord{
			ID: workID, Kind: "task", Title: "First", State: "closed",
			CreatedAt: now, UpdatedAt: later,
		})
	}); err != nil {
		t.Fatalf("store: re-upsert work: %v", err)
	}
	var createdAt string
	err = db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT kind, state, created_at FROM works WHERE id=?`, string(workID)).Scan(&kind, &state, &createdAt)
	})
	if err != nil {
		t.Fatalf("store: read re-upserted work: %v", err)
	}
	if state != "closed" || kind != "task" {
		t.Errorf("store: re-upserted work = %s/%s, want task/closed (kind preserved, state replaced)", kind, state)
	}
}

func TestSetMetaReplacesValue(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.SetMeta(ctx, "k", "v1")
	}); err != nil {
		t.Fatalf("store: set meta: %v", err)
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.SetMeta(ctx, "k", "v2")
	}); err != nil {
		t.Fatalf("store: set meta again: %v", err)
	}
	var value string
	err := db.View(ctx, func(tx *Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "k").Scan(&value)
	})
	if err != nil {
		t.Fatalf("store: read meta: %v", err)
	}
	if value != "v2" {
		t.Errorf("store: meta value = %q, want v2 (replaced, not duplicated)", value)
	}
}

func TestOperationLookupAndPolicyTransition(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()
	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("store: operation id: %v", err)
	}
	now := time.Now().UTC()
	rec := OperationRecord{
		ID: opID, Kind: "test.op", State: OpPending, WorkID: "work",
		Generation: 1, Policy: "roll_forward", Payload: []byte(`{"k":1}`),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.InsertOperation(ctx, rec)
	}); err != nil {
		t.Fatalf("store: insert operation: %v", err)
	}

	got, err := db.Operation(ctx, opID)
	if err != nil {
		t.Fatalf("store: lookup operation: %v", err)
	}
	if got.ID != opID || got.Kind != "test.op" || got.State != OpPending || got.Policy != "roll_forward" {
		t.Errorf("store: operation = %+v, want id/kind/state/policy round trip", got)
	}

	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.SetOperationPolicy(ctx, opID, "roll_back")
	}); err != nil {
		t.Fatalf("store: set policy: %v", err)
	}
	got, err = db.Operation(ctx, opID)
	if err != nil {
		t.Fatalf("store: lookup operation after policy change: %v", err)
	}
	if got.Policy != "roll_back" {
		t.Errorf("store: policy = %q, want roll_back", got.Policy)
	}

	if _, err := db.Operation(ctx, "00000000-0000-4000-8000-000000000000"); err == nil {
		t.Error("store: lookup of unknown operation succeeded, want error")
	}
	if err := db.Update(ctx, func(tx *Tx) error {
		return tx.SetOperationPolicy(ctx, "00000000-0000-4000-8000-000000000000", "roll_back")
	}); !errors.Is(err, ErrNoJournalRow) {
		t.Errorf("store: policy update on unknown operation = %v, want ErrNoJournalRow", err)
	}
}
