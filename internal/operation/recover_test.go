package operation

import (
	"context"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/store"
)

func TestRecoverRollBackRevertsAppliedEffectsInReverseOrder(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}

	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	if got, want := e.rec.revertedSeqs(), []int64{2, 1}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("operation: reverts = %v, want %v (applied effects in reverse order)", got, want)
	}
	if applies := e.rec.appliedSeqs(); len(applies) != 2 {
		t.Errorf("operation: applies = %v, want only the two applied before the crash", applies)
	}

	states := effectStates(t, e.db, e.op.ID())
	for seq := int64(1); seq <= 3; seq++ {
		if states[seq] != store.EffectReverted {
			t.Errorf("operation: effect %d state = %s, want %s", seq, states[seq], store.EffectReverted)
		}
	}
	pending, err := e.db.PendingOperations(context.Background())
	if err != nil {
		t.Fatalf("operation: list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("operation: rolled-back operation still pending: %+v", pending)
	}
}

func TestRecoverRollBackWithNothingApplied(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	setFailpoint(t, "prepared", 1)
	mustCrash(t, e.mgr, e.op)

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	if reverts := e.rec.revertedSeqs(); len(reverts) != 0 {
		t.Errorf("operation: reverts = %v, want none (no effect was applied)", reverts)
	}
	if applies := e.rec.appliedSeqs(); len(applies) != 0 {
		t.Errorf("operation: applies = %v, want none", applies)
	}
}

func TestRecoverPendingIsIdempotent(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)

	mgr := e.reopen(t, true)
	if err := mgr.RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: first recover: %v", err)
	}
	if err := mgr.RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: second recover: %v", err)
	}

	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after two recoveries = %s, want %s", state, store.OpFinalized)
	}
	got := e.rec.appliedSeqs()
	if len(got) != 3 {
		t.Errorf("operation: applies across crash and two recoveries = %v, want each of 1,2,3 exactly once", got)
	}
	states := effectStates(t, e.db, e.op.ID())
	for seq := int64(1); seq <= 3; seq++ {
		if states[seq] != store.EffectApplied {
			t.Errorf("operation: effect %d state = %s, want %s", seq, states[seq], store.EffectApplied)
		}
	}
}

func TestRecoverRollBackIdempotent(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	setFailpoint(t, "effect-applied", 3)
	mustCrash(t, e.mgr, e.op)

	mgr := e.reopen(t, true)
	if err := mgr.RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: first recover: %v", err)
	}
	if err := mgr.RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: second recover: %v", err)
	}

	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after two recoveries = %s, want %s", state, store.OpRolledBack)
	}
	if got, want := e.rec.revertedSeqs(), []int64{3, 2, 1}; len(got) != len(want) {
		t.Errorf("operation: reverts = %v, want %v exactly once", got, want)
	}
}

func TestRecoverUnknownEffectKindFailsLoudly(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	setFailpoint(t, "prepared", 1)
	mustCrash(t, e.mgr, e.op)

	err := e.reopen(t, false).RecoverPending(context.Background())
	if err == nil {
		t.Fatal("operation: recovery without a registered effect kind succeeded, want error")
	}
	if !strings.Contains(err.Error(), "test.map") {
		t.Errorf("operation: recovery error does not name the missing kind: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpPrepared {
		t.Errorf("operation: state after failed recovery = %s, want %s (untouched)", state, store.OpPrepared)
	}
}

func TestRecoverPendingWithoutPendingOperationsIsNoOp(t *testing.T) {
	e := newEnv(t, 2, RollForward)
	if err := e.mgr.Run(context.Background(), e.op); err != nil {
		t.Fatalf("operation: run: %v", err)
	}
	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if got := e.rec.appliedSeqs(); len(got) != 2 {
		t.Errorf("operation: applies after no-op recovery = %v, want 2", got)
	}
}
