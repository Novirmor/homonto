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

func TestRecoverCrashDuringRollForwardConverges(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	restore := setFailpoint(t, "effect-applied", 1)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Interrupt recovery mid-roll-forward: it re-applies effect 2, commits
	// its row, then dies at the effect-applied boundary.
	restore = setFailpoint(t, "effect-applied", 1)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectApplied || states[3] != store.EffectPending {
		t.Fatalf("operation: effect states after interrupted recovery = %v, want applied/applied/pending", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpFinalized)
	}
	for seq := int64(1); seq <= 3; seq++ {
		if got := e.rec.applyCount(seq); got != 1 {
			t.Errorf("operation: effect %d applied %d times across crash and re-recovery, want 1", seq, got)
		}
	}
}

func TestRecoverCrashAtFinalizeDuringRollForwardIsNoOpOnRerun(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	restore := setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Recovery finishes roll-forward, commits finalized, then dies.
	restore = setFailpoint(t, "finalized", 1)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpFinalized)
	}
	if got := e.rec.appliedSeqs(); len(got) != 3 {
		t.Errorf("operation: applies after re-recovery = %v, want 3 total, none duplicated", got)
	}
}

func TestRecoverCrashDuringRollBackConverges(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	restore := setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Interrupt recovery mid-roll-back: the reversed loop first closes
	// effect 3 (never applied, no Revert), commits its reverted row, then
	// dies at the effect-reverted boundary.
	restore = setFailpoint(t, "effect-reverted", 1)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectApplied || states[3] != store.EffectReverted {
		t.Fatalf("operation: effect states after interrupted recovery = %v, want applied/applied/reverted", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpRolledBack)
	}
	// Reverts resume in reverse order exactly where the crash left them.
	if got, want := e.rec.revertedSeqs(), []int64{2, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("operation: reverts = %v, want %v each exactly once", got, want)
	}
}

func TestRecoverCrashAfterRollBackCompletesIsNoOpOnRerun(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	restore := setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Recovery completes the whole roll-back, commits rolled_back, dies.
	restore = setFailpoint(t, "rolled-back", 1)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpRolledBack)
	}
	if got, want := e.rec.revertedSeqs(), []int64{2, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("operation: reverts = %v, want %v each exactly once", got, want)
	}
}

func TestRecoverCrashWhileAbortingPendingOperationConverges(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	restore := setFailpoint(t, "pending", 1)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Interrupted recovery on the pending-abort path: the rolled_back
	// commit lands, then the boundary hook kills the process.
	restore = setFailpoint(t, "rolled-back", 1)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpRolledBack)
	}
	if applies := e.rec.appliedSeqs(); len(applies) != 0 {
		t.Errorf("operation: applies = %v, want none", applies)
	}
	if reverts := e.rec.revertedSeqs(); len(reverts) != 0 {
		t.Errorf("operation: reverts = %v, want none", reverts)
	}
}

func TestRecoverCrashInUnrecordedRevertWindowConverges(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	restore := setFailpoint(t, "effect-applied", 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Interrupted recovery on the revert path: the reversed loop closes
	// effect 3 (never applied, no Revert), reverts effect 2, then dies
	// before effect 2's reverted row is committed.
	restore = setUnrecordedRevertFailpoint(t, e.op.ID(), 2)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	// Effect 2 was reverted but its row still reads applied, so the next
	// pass must revert it again — the idempotency contract on Revert.
	states := effectStates(t, e.db, e.op.ID())
	if states[3] != store.EffectReverted || states[2] != store.EffectApplied || states[1] != store.EffectApplied {
		t.Fatalf("operation: effect states after unrecorded revert crash = %v, want applied/applied/reverted", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpRolledBack)
	}
	// Effect 2 reverted exactly twice (once per pass) and still preceded
	// effect 1: reverse order preserved across the crash.
	if got, want := e.rec.revertedSeqs(), []int64{2, 2, 1}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("operation: reverts = %v, want %v (effect 2 reverted twice, reverse order kept)", got, want)
	}
	states = effectStates(t, e.db, e.op.ID())
	for seq := int64(1); seq <= 3; seq++ {
		if states[seq] != store.EffectReverted {
			t.Errorf("operation: effect %d state = %s, want %s", seq, states[seq], store.EffectReverted)
		}
	}
}

func TestRecoverCrashInUnrecordedApplyWindowConverges(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	restore := setFailpoint(t, "effect-applied", 1)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Recovery re-applies effect 3, dies before committing its row.
	restore = setUnrecordedApplyFailpoint(t, e.op.ID(), 3)
	mustRecoverCrash(t, e.reopen(t, true))
	restore()

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: re-recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after re-recovery = %s, want %s", state, store.OpFinalized)
	}
	for seq, want := range map[int64]int{1: 1, 2: 1, 3: 2} {
		if got := e.rec.applyCount(seq); got != want {
			t.Errorf("operation: effect %d applied %d times, want %d (idempotent re-apply after unrecorded window)", seq, got, want)
		}
	}
}
