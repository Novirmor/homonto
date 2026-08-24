package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

func TestOperationJournalLifecycle(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()

	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("store: generate operation id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("store: generate work id: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	rec := OperationRecord{
		ID:         opID,
		Kind:       "test.op",
		State:      OpPending,
		WorkID:     workID,
		Generation: 3,
		Policy:     "roll_forward",
		Payload:    json.RawMessage(`{"n":1}`),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	err = db.Update(ctx, func(tx *Tx) error {
		return tx.InsertOperation(ctx, rec)
	})
	if err != nil {
		t.Fatalf("store: insert operation: %v", err)
	}

	pending, err := db.PendingOperations(ctx)
	if err != nil {
		t.Fatalf("store: list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("store: pending operations = %d, want 1", len(pending))
	}
	got := pending[0]
	if got.ID != opID || got.Kind != "test.op" || got.State != OpPending ||
		got.WorkID != workID || got.Generation != 3 || got.Policy != "roll_forward" {
		t.Errorf("store: pending record mismatch: %+v", got)
	}
	if string(got.Payload) != `{"n":1}` {
		t.Errorf("store: payload = %s, want {\"n\":1}", got.Payload)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("store: timestamps not round-tripped: created=%v updated=%v want %v",
			got.CreatedAt, got.UpdatedAt, now)
	}

	setState := func(state string) {
		t.Helper()
		err := db.Update(ctx, func(tx *Tx) error {
			return tx.SetOperationState(ctx, opID, state)
		})
		if err != nil {
			t.Fatalf("store: set state %s: %v", state, err)
		}
	}

	setState(OpPrepared)
	pending, err = db.PendingOperations(ctx)
	if err != nil {
		t.Fatalf("store: list pending after prepare: %v", err)
	}
	if len(pending) != 1 || pending[0].State != OpPrepared {
		t.Fatalf("store: prepared operation not listed as pending: %+v", pending)
	}
	if !pending[0].UpdatedAt.After(now) {
		t.Error("store: updated_at not advanced by SetOperationState")
	}

	setState(OpFinalized)
	pending, err = db.PendingOperations(ctx)
	if err != nil {
		t.Fatalf("store: list pending after finalize: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("store: finalized operation still pending: %+v", pending)
	}
}

func TestSetOperationStateUnknownIDFails(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("store: generate operation id: %v", err)
	}
	err = db.Update(context.Background(), func(tx *Tx) error {
		return tx.SetOperationState(context.Background(), opID, OpFinalized)
	})
	if err == nil {
		t.Error("store: SetOperationState for unknown id succeeded, want error")
	}
}

func TestEffectRowsLifecycle(t *testing.T) {
	db := openRW(t, filepath.Join(t.TempDir(), "runtime.sqlite"))
	ctx := context.Background()

	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("store: generate operation id: %v", err)
	}
	err = db.Update(ctx, func(tx *Tx) error {
		return tx.InsertOperation(ctx, OperationRecord{
			ID: opID, Kind: "test.op", State: OpPending, Policy: "roll_back",
			Payload: json.RawMessage(`{}`), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	})
	if err != nil {
		t.Fatalf("store: insert operation: %v", err)
	}

	for seq := int64(1); seq <= 3; seq++ {
		err = db.Update(ctx, func(tx *Tx) error {
			return tx.InsertEffect(ctx, EffectRow{
				OpID: opID, Seq: seq, Kind: "test.effect", State: EffectPending,
				Payload: json.RawMessage(`{"token":"t"}`),
			})
		})
		if err != nil {
			t.Fatalf("store: insert effect %d: %v", seq, err)
		}
	}

	rows, err := db.OperationEffects(ctx, opID)
	if err != nil {
		t.Fatalf("store: list effects: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("store: effects = %d, want 3", len(rows))
	}
	for i, row := range rows {
		if row.Seq != int64(i+1) || row.Kind != "test.effect" || row.State != EffectPending {
			t.Errorf("store: effect row %d mismatch: %+v", i+1, row)
		}
		if string(row.Payload) != `{"token":"t"}` {
			t.Errorf("store: effect %d payload = %s", row.Seq, row.Payload)
		}
	}

	err = db.Update(ctx, func(tx *Tx) error {
		return tx.SetEffectState(ctx, opID, 2, EffectApplied)
	})
	if err != nil {
		t.Fatalf("store: set effect state: %v", err)
	}
	rows, err = db.OperationEffects(ctx, opID)
	if err != nil {
		t.Fatalf("store: list effects after update: %v", err)
	}
	want := []string{EffectPending, EffectApplied, EffectPending}
	for i, row := range rows {
		if row.State != want[i] {
			t.Errorf("store: effect %d state = %s, want %s", row.Seq, row.State, want[i])
		}
	}

	err = db.Update(ctx, func(tx *Tx) error {
		return tx.SetEffectState(ctx, opID, 99, EffectApplied)
	})
	if err == nil {
		t.Error("store: SetEffectState for unknown seq succeeded, want error")
	}
}
