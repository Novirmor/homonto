package operation

import (
	"context"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

func TestRecoverOneTargetsOnlyTheNamedOperation(t *testing.T) {
	e := newEnv(t, 2, RollForward)
	restore := setFailpoint(t, "effect-applied", 1)
	mustCrash(t, e.mgr, e.op)
	restore()

	op2 := &testOp{
		id:      mustOpID(t),
		work:    e.op.WorkID(),
		policy:  RollForward,
		effects: []Effect{&mapEffect{rec: e.rec}, &mapEffect{rec: e.rec}},
	}
	restore = setFailpoint(t, "effect-applied", 1)
	mustCrash(t, e.mgr, op2)
	restore()

	if err := e.mgr.RecoverOne(context.Background(), e.op.ID()); err != nil {
		t.Fatalf("operation: RecoverOne: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: first op state = %s, want %s", state, store.OpFinalized)
	}
	if state := opState(t, e.db, op2.ID()); state != store.OpPrepared {
		t.Errorf("operation: sibling op state = %s, want %s (untouched by RecoverOne)", state, store.OpPrepared)
	}

	if err := e.mgr.RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: RecoverPending: %v", err)
	}
	if state := opState(t, e.db, op2.ID()); state != store.OpFinalized {
		t.Errorf("operation: sibling op state after pass = %s, want %s", state, store.OpFinalized)
	}
}

func TestRecoverOneOnTerminalOperationIsNoOp(t *testing.T) {
	e := newEnv(t, 1, RollForward)
	if err := e.mgr.Run(context.Background(), e.op); err != nil {
		t.Fatalf("operation: run: %v", err)
	}
	if err := e.mgr.RecoverOne(context.Background(), e.op.ID()); err != nil {
		t.Fatalf("operation: RecoverOne on finalized op: %v", err)
	}
	if got := e.rec.appliedSeqs(); len(got) != 1 {
		t.Errorf("operation: applies after RecoverOne on finalized op = %v, want 1", got)
	}
}

func TestRecoverOneOnUnknownOperationFails(t *testing.T) {
	e := newEnv(t, 1, RollForward)
	id, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("operation: id: %v", err)
	}
	if err := e.mgr.RecoverOne(context.Background(), id); err == nil {
		t.Error("operation: RecoverOne on unknown operation succeeded, want error")
	}
}

func TestSetFailpointHookRestoresPreviousHook(t *testing.T) {
	e := newEnv(t, 1, RollForward)
	var points []string
	restoreA := SetFailpointHook(func(p string) { points = append(points, p) })
	restoreB := SetFailpointHook(func(p string) {})
	restoreB()
	if err := e.mgr.Run(context.Background(), e.op); err != nil {
		t.Fatalf("operation: run: %v", err)
	}
	restoreA()
	if len(points) < 3 {
		t.Errorf("operation: hook recorded %d points %v, want pending/prepared/applied/finalized sequence", len(points), points)
	}
}

func mustOpID(t *testing.T) identity.OperationID {
	t.Helper()
	id, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("operation: id: %v", err)
	}
	return id
}
