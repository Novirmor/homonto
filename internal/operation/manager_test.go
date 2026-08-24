package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// appliedCall records one Apply invocation: which journal position ran and
// the token carried by the persisted payload.
type appliedCall struct {
	Seq   int64
	Token string
}

// recorder is the in-memory test double behind mapEffect. It proves the
// protocol's ordering and identity claims: Prepare mints tokens in call
// order, Apply replays them from the persisted record, Revert runs in the
// order recovery chooses.
type recorder struct {
	mu        sync.Mutex
	prepCount int
	applies   []appliedCall
	reverts   []int64
	failApply map[int64]error
}

func (r *recorder) nextToken() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepCount++
	return fmt.Sprintf("token-%d", r.prepCount)
}

func (r *recorder) apply(rec EffectRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err, ok := r.failApply[rec.Seq]; ok {
		return err
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		return fmt.Errorf("test double: decode payload of effect %d: %w", rec.Seq, err)
	}
	r.applies = append(r.applies, appliedCall{Seq: rec.Seq, Token: payload.Token})
	return nil
}

func (r *recorder) revert(rec EffectRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reverts = append(r.reverts, rec.Seq)
	return nil
}

func (r *recorder) appliedSeqs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	seqs := make([]int64, len(r.applies))
	for i, c := range r.applies {
		seqs[i] = c.Seq
	}
	return seqs
}

func (r *recorder) tokenFor(seq int64) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.applies {
		if c.Seq == seq {
			return c.Token, true
		}
	}
	return "", false
}

func (r *recorder) revertedSeqs() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.reverts...)
}

// applyCount reports how many times the effect at journal position seq has
// been applied (an unrecorded crash window makes double-apply the documented
// contract, so tests count rather than assert presence).
func (r *recorder) applyCount(seq int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.applies {
		if c.Seq == seq {
			n++
		}
	}
	return n
}

// tokensFor returns the token of every Apply of the effect at journal
// position seq, in call order.
func (r *recorder) tokensFor(seq int64) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var tokens []string
	for _, c := range r.applies {
		if c.Seq == seq {
			tokens = append(tokens, c.Token)
		}
	}
	return tokens
}

// mapEffect is the in-memory effect double: Prepare mints a token, Apply
// records it from the journal record, Revert records the journal position.
type mapEffect struct {
	rec *recorder
}

func (e *mapEffect) Kind() string { return "test.map" }

func (e *mapEffect) Prepare(ctx context.Context) (any, error) {
	return map[string]string{"token": e.rec.nextToken()}, nil
}

func (e *mapEffect) Apply(ctx context.Context, rec EffectRecord) error {
	return e.rec.apply(rec)
}

func (e *mapEffect) Revert(ctx context.Context, rec EffectRecord) error {
	return e.rec.revert(rec)
}

// testOp is a minimal Operation with nEffects mapEffects under one recorder.
type testOp struct {
	id      identity.OperationID
	work    identity.WorkID
	policy  Policy
	effects []Effect
}

func (o *testOp) ID() identity.OperationID { return o.id }
func (o *testOp) Kind() string             { return "test.op" }
func (o *testOp) WorkID() identity.WorkID  { return o.work }
func (o *testOp) Generation() int64        { return 1 }
func (o *testOp) Policy() Policy           { return o.policy }
func (o *testOp) Payload() any             { return map[string]int{"effects": len(o.effects)} }
func (o *testOp) Effects() []Effect        { return o.effects }

// env wires one test database, manager, recorder, and operation together.
type env struct {
	path string
	db   *store.DB
	mgr  *Manager
	rec  *recorder
	op   Operation
}

func newEnv(t *testing.T, nEffects int, policy Policy) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db, err := store.Open(context.Background(), path, store.OpenOptions{})
	if err != nil {
		t.Fatalf("operation: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rec := &recorder{}
	e := &env{path: path, db: db, mgr: NewManager(db), rec: rec}
	e.mgr.RegisterEffect(&mapEffect{rec: rec})
	opID, err := identity.NewOperationID()
	if err != nil {
		t.Fatalf("operation: generate operation id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("operation: generate work id: %v", err)
	}
	effects := make([]Effect, nEffects)
	for i := range effects {
		effects[i] = &mapEffect{rec: rec}
	}
	e.op = &testOp{id: opID, work: workID, policy: policy, effects: effects}
	return e
}

// reopen simulates a new process against the same database: the old handle
// is closed, a fresh manager is built, and its effect registry is populated.
func (e *env) reopen(t *testing.T, register bool) *Manager {
	t.Helper()
	if err := e.db.Close(); err != nil {
		t.Fatalf("operation: close store: %v", err)
	}
	db, err := store.Open(context.Background(), e.path, store.OpenOptions{})
	if err != nil {
		t.Fatalf("operation: reopen store: %v", err)
	}
	e.db = db
	mgr := NewManager(db)
	if register {
		mgr.RegisterEffect(&mapEffect{rec: e.rec})
	}
	return mgr
}

// setFailpointHook installs an arbitrary failNow hook and returns a restore
// function (also run at test cleanup, LIFO with later installs). Tests that
// crash at one boundary and then drive recovery cleanly restore explicitly
// before the recovery run.
func setFailpointHook(t *testing.T, hook func(point string)) (restore func()) {
	t.Helper()
	prev := failNow
	var once sync.Once
	restore = func() {
		once.Do(func() { failNow = prev })
	}
	failNow = hook
	t.Cleanup(restore)
	return restore
}

// setFailpoint installs a failNow hook that panics (simulating process death
// at the boundary) the nth time the named point is reached. The returned
// restore function clears the hook before a clean recovery run.
func setFailpoint(t *testing.T, point string, nth int) (restore func()) {
	t.Helper()
	counts := map[string]int{}
	return setFailpointHook(t, func(p string) {
		if p != point {
			return
		}
		counts[p]++
		if counts[p] == nth {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// setUnrecordedApplyFailpoint crashes at the unrecorded-apply window of one
// journal position: after Apply performed but before its row is committed.
func setUnrecordedApplyFailpoint(t *testing.T, opID identity.OperationID, seq int64) (restore func()) {
	t.Helper()
	point := fmt.Sprintf("effect-applied-unrecorded:%s:%d", opID, seq)
	return setFailpointHook(t, func(p string) {
		if p == point {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// setUnrecordedApplyFailureFailpoint crashes at the unrecorded apply-failure
// window of one journal position: after Apply returned an error but before
// its failed row is committed.
func setUnrecordedApplyFailureFailpoint(t *testing.T, opID identity.OperationID, seq int64) (restore func()) {
	t.Helper()
	point := fmt.Sprintf("effect-failed-unrecorded:%s:%d", opID, seq)
	return setFailpointHook(t, func(p string) {
		if p == point {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// setUnrecordedRevertFailpoint crashes at the unrecorded-revert window of
// one journal position: after Revert returned but before its row is
// committed.
func setUnrecordedRevertFailpoint(t *testing.T, opID identity.OperationID, seq int64) (restore func()) {
	t.Helper()
	point := fmt.Sprintf("effect-reverted-unrecorded:%s:%d", opID, seq)
	return setFailpointHook(t, func(p string) {
		if p == point {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// mustCrash runs op and fails the test unless the failpoint panicked.
func mustCrash(t *testing.T, mgr *Manager, op Operation) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := mgr.Run(context.Background(), op); err != nil {
			panic(fmt.Sprintf("Run returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("operation: expected simulated crash at failpoint")
	}
}

// mustRecoverCrash runs RecoverPending and fails the test unless the
// failpoint panicked mid-recovery.
func mustRecoverCrash(t *testing.T, mgr *Manager) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := mgr.RecoverPending(context.Background()); err != nil {
			panic(fmt.Sprintf("RecoverPending returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("operation: expected simulated crash at recovery failpoint")
	}
}

// opState reads the operation's persisted state.
func opState(t *testing.T, db *store.DB, id identity.OperationID) string {
	t.Helper()
	var state string
	err := db.View(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT state FROM operations WHERE id=?`, string(id)).Scan(&state)
	})
	if err != nil {
		t.Fatalf("operation: read state of %s: %v", id, err)
	}
	return state
}

// opPolicy reads the operation's persisted crash-recovery policy.
func opPolicy(t *testing.T, db *store.DB, id identity.OperationID) string {
	t.Helper()
	var policy string
	err := db.View(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT policy FROM operations WHERE id=?`, string(id)).Scan(&policy)
	})
	if err != nil {
		t.Fatalf("operation: read policy of %s: %v", id, err)
	}
	return policy
}

// effectStates reads every effect row's persisted state keyed by seq.
func effectStates(t *testing.T, db *store.DB, id identity.OperationID) map[int64]string {
	t.Helper()
	states := map[int64]string{}
	err := db.View(context.Background(), func(tx *store.Tx) error {
		rows, err := tx.QueryContext(context.Background(),
			`SELECT seq, state FROM operation_effects WHERE op_id=? ORDER BY seq`, string(id))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seq int64
			var state string
			if err := rows.Scan(&seq, &state); err != nil {
				return err
			}
			states[seq] = state
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("operation: read effect states of %s: %v", id, err)
	}
	return states
}

func TestRunAppliesEffectsInOrderAndFinalizes(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	if err := e.mgr.Run(context.Background(), e.op); err != nil {
		t.Fatalf("operation: run: %v", err)
	}

	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state = %s, want %s", state, store.OpFinalized)
	}
	got := e.rec.appliedSeqs()
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("operation: applies = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operation: applies = %v, want %v", got, want)
		}
	}
	for seq := int64(1); seq <= 3; seq++ {
		token, ok := e.rec.tokenFor(seq)
		if !ok || token != fmt.Sprintf("token-%d", seq) {
			t.Errorf("operation: effect %d applied with token %q, want token-%d", seq, token, seq)
		}
	}
	if reverts := e.rec.revertedSeqs(); len(reverts) != 0 {
		t.Errorf("operation: unexpected reverts %v", reverts)
	}
	pending, err := e.db.PendingOperations(context.Background())
	if err != nil {
		t.Fatalf("operation: list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("operation: finalized operation still pending: %+v", pending)
	}
}

func TestRunDuplicateIDFails(t *testing.T) {
	e := newEnv(t, 1, RollForward)
	if err := e.mgr.Run(context.Background(), e.op); err != nil {
		t.Fatalf("operation: first run: %v", err)
	}
	if err := e.mgr.Run(context.Background(), e.op); err == nil {
		t.Error("operation: re-running an operation with the same ID succeeded, want error")
	}
}

func TestRunCrashBeforePrepareCommitAbortsOperation(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	setFailpoint(t, "pending", 1)
	mustCrash(t, e.mgr, e.op)

	if state := opState(t, e.db, e.op.ID()); state != store.OpPending {
		t.Fatalf("operation: state after crash = %s, want %s", state, store.OpPending)
	}
	if states := effectStates(t, e.db, e.op.ID()); len(states) != 0 {
		t.Errorf("operation: effects journalled before prepare commit: %v", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	if applies := e.rec.appliedSeqs(); len(applies) != 0 {
		t.Errorf("operation: effects applied for unprepared operation: %v", applies)
	}
	if reverts := e.rec.revertedSeqs(); len(reverts) != 0 {
		t.Errorf("operation: reverts for unprepared operation: %v", reverts)
	}
}

func TestRunCrashAfterPrepareRollsForward(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	setFailpoint(t, "prepared", 1)
	mustCrash(t, e.mgr, e.op)

	if state := opState(t, e.db, e.op.ID()); state != store.OpPrepared {
		t.Fatalf("operation: state after crash = %s, want %s", state, store.OpPrepared)
	}
	states := effectStates(t, e.db, e.op.ID())
	if len(states) != 3 || states[1] != store.EffectPending {
		t.Fatalf("operation: effect states after prepare = %v, want 3 pending rows", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpFinalized)
	}
	for seq := int64(1); seq <= 3; seq++ {
		token, ok := e.rec.tokenFor(seq)
		if !ok || token != fmt.Sprintf("token-%d", seq) {
			t.Errorf("operation: effect %d recovered with token %q, want token-%d (identity not preserved)", seq, token, seq)
		}
	}
}

func TestRunCrashAfterEffectAppliedRollsForward(t *testing.T) {
	for _, crashAfter := range []int64{1, 2, 3} {
		t.Run(fmt.Sprintf("after effect %d", crashAfter), func(t *testing.T) {
			e := newEnv(t, 3, RollForward)
			setFailpoint(t, "effect-applied", int(crashAfter))
			mustCrash(t, e.mgr, e.op)

			states := effectStates(t, e.db, e.op.ID())
			for seq := int64(1); seq <= 3; seq++ {
				want := store.EffectPending
				if seq <= crashAfter {
					want = store.EffectApplied
				}
				if states[seq] != want {
					t.Fatalf("operation: effect %d state after crash = %s, want %s", seq, states[seq], want)
				}
			}

			if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
				t.Fatalf("operation: recover: %v", err)
			}
			if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
				t.Errorf("operation: state after recovery = %s, want %s", state, store.OpFinalized)
			}
			got := e.rec.appliedSeqs()
			if len(got) != 3 {
				t.Fatalf("operation: applies = %v, want each of 1,2,3 exactly once", got)
			}
			for i, seq := range []int64{1, 2, 3} {
				if got[i] != seq {
					t.Fatalf("operation: applies = %v, want 1,2,3 in order", got)
				}
				token, _ := e.rec.tokenFor(seq)
				if token != fmt.Sprintf("token-%d", seq) {
					t.Errorf("operation: effect %d token = %q, want token-%d", seq, token, seq)
				}
			}
		})
	}
}

func TestRunCrashAfterFinalizeNeedsNoRecovery(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	setFailpoint(t, "finalized", 1)
	mustCrash(t, e.mgr, e.op)

	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Fatalf("operation: state after crash = %s, want %s", state, store.OpFinalized)
	}
	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if got := e.rec.appliedSeqs(); len(got) != 3 {
		t.Errorf("operation: recovery re-applied finalized effects: %v", got)
	}
}

// TestRunApplyFailureMarksEffectFailedAndRollsBackOnRecover proves the
// error-path contract: a failed Apply journals its effect row failed and
// switches the operation to roll_back before Run returns, so recovery
// never re-applies the failed effect — it reverts the earlier applied
// effects and closes the operation rolled back.
func TestRunApplyFailureMarksEffectFailedAndRollsBackOnRecover(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	e.rec.failApply = map[int64]error{2: errors.New("effect 2 boom")}
	err := e.mgr.Run(context.Background(), e.op)
	if err == nil {
		t.Fatal("operation: run with failing effect succeeded, want error")
	}

	if state := opState(t, e.db, e.op.ID()); state != store.OpPrepared {
		t.Fatalf("operation: state after apply failure = %s, want %s", state, store.OpPrepared)
	}
	if policy := opPolicy(t, e.db, e.op.ID()); policy != string(RollBack) {
		t.Fatalf("operation: policy after apply failure = %s, want %s", policy, RollBack)
	}
	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectFailed || states[3] != store.EffectPending {
		t.Errorf("operation: effect states after apply failure = %v, want applied/failed/pending", states)
	}

	e.rec.mu.Lock()
	e.rec.failApply = nil
	e.rec.mu.Unlock()
	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	// The failed effect is never re-applied; only the applied one reverts.
	if got := e.rec.applyCount(2); got != 0 {
		t.Errorf("operation: failed effect applied %d times after recovery, want 0 (never re-applied)", got)
	}
	if got, want := e.rec.revertedSeqs(), []int64{1}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("operation: reverts = %v, want %v", got, want)
	}
	// The failed marker survives roll-back as the row's terminal state.
	states = effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectReverted || states[2] != store.EffectFailed || states[3] != store.EffectReverted {
		t.Errorf("operation: effect states after recovery = %v, want reverted/failed/reverted", states)
	}
}

// TestRunCrashInUnrecordedApplyFailureWindow crashes between a failed
// Apply's return and its failed row's commit: the row stays pending under
// roll_forward, and recovery re-applies it under the ordinary idempotency
// contract (for the gitx cherry-pick that means recognizing its own
// conflicted stop — proven in package gitx).
func TestRunCrashInUnrecordedApplyFailureWindow(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	e.rec.failApply = map[int64]error{2: errors.New("effect 2 boom")}
	restore := setUnrecordedApplyFailureFailpoint(t, e.op.ID(), 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectPending || states[3] != store.EffectPending {
		t.Fatalf("operation: effect states after unrecorded crash = %v, want applied/pending/pending", states)
	}
	if policy := opPolicy(t, e.db, e.op.ID()); policy != string(RollForward) {
		t.Fatalf("operation: policy after unrecorded crash = %s, want %s", policy, RollForward)
	}

	// The transient failure is gone: the pending row re-applies cleanly.
	e.rec.mu.Lock()
	e.rec.failApply = nil
	e.rec.mu.Unlock()
	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpFinalized)
	}
	for seq := int64(1); seq <= 3; seq++ {
		if got := e.rec.applyCount(seq); got != 1 {
			t.Errorf("operation: effect %d applied %d times, want 1", seq, got)
		}
	}
}

// TestRunCrashAfterFailedRowBeforePolicySwitch crashes after the failed
// row committed but before the roll_back policy did: recovery finds a
// prepared roll_forward operation holding a failed row, switches it to
// roll-back, and closes it there — the failed effect is never re-applied.
func TestRunCrashAfterFailedRowBeforePolicySwitch(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	e.rec.failApply = map[int64]error{2: errors.New("effect 2 boom")}
	restore := setFailpoint(t, "effect-failed", 1)
	mustCrash(t, e.mgr, e.op)
	restore()

	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectFailed || states[3] != store.EffectPending {
		t.Fatalf("operation: effect states after crash = %v, want applied/failed/pending", states)
	}
	if policy := opPolicy(t, e.db, e.op.ID()); policy != string(RollForward) {
		t.Fatalf("operation: policy after crash = %s, want %s (switch not yet committed)", policy, RollForward)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	if got := e.rec.applyCount(2); got != 0 {
		t.Errorf("operation: failed effect applied %d times, want 0 (never re-applied)", got)
	}
	if got, want := e.rec.revertedSeqs(), []int64{1}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("operation: reverts = %v, want %v", got, want)
	}
	states = effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectReverted || states[2] != store.EffectFailed || states[3] != store.EffectReverted {
		t.Errorf("operation: effect states after recovery = %v, want reverted/failed/reverted", states)
	}
}

func TestConcurrentRunsSerialize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sqlite")
	db, err := store.Open(context.Background(), path, store.OpenOptions{})
	if err != nil {
		t.Fatalf("operation: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mgr := NewManager(db)

	newOp := func(nEffects int) (Operation, *recorder) {
		opID, err := identity.NewOperationID()
		if err != nil {
			t.Fatalf("operation: generate operation id: %v", err)
		}
		workID, err := identity.NewWorkID()
		if err != nil {
			t.Fatalf("operation: generate work id: %v", err)
		}
		rec := &recorder{}
		effects := make([]Effect, nEffects)
		for i := range effects {
			effects[i] = &mapEffect{rec: rec}
		}
		return &testOp{id: opID, work: workID, policy: RollForward, effects: effects}, rec
	}
	opA, recA := newOp(3)
	opB, recB := newOp(2)

	var wg sync.WaitGroup
	for _, op := range []Operation{opA, opB} {
		wg.Add(1)
		go func(op Operation) {
			defer wg.Done()
			if err := mgr.Run(context.Background(), op); err != nil {
				t.Errorf("operation: concurrent run: %v", err)
			}
		}(op)
	}
	wg.Wait()

	if state := opState(t, db, opA.ID()); state != store.OpFinalized {
		t.Errorf("operation: op A state = %s, want %s", state, store.OpFinalized)
	}
	if state := opState(t, db, opB.ID()); state != store.OpFinalized {
		t.Errorf("operation: op B state = %s, want %s", state, store.OpFinalized)
	}
	if got := recA.appliedSeqs(); len(got) != 3 {
		t.Errorf("operation: op A applies = %v, want 3", got)
	}
	if got := recB.appliedSeqs(); len(got) != 2 {
		t.Errorf("operation: op B applies = %v, want 2", got)
	}
	pending, err := db.PendingOperations(context.Background())
	if err != nil {
		t.Fatalf("operation: list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("operation: pending after concurrent runs = %+v, want none", pending)
	}
}

func TestRunCrashInUnrecordedApplyWindowRollForwardAppliesTwice(t *testing.T) {
	e := newEnv(t, 3, RollForward)
	restore := setUnrecordedApplyFailpoint(t, e.op.ID(), 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	// Effect 2's side effect performed but its row still reads pending.
	states := effectStates(t, e.db, e.op.ID())
	if states[1] != store.EffectApplied || states[2] != store.EffectPending || states[3] != store.EffectPending {
		t.Fatalf("operation: effect states after unrecorded crash = %v, want applied/pending/pending", states)
	}

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpFinalized {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpFinalized)
	}
	// The idempotency contract, not exactly-once: effect 2 applied twice.
	for seq, want := range map[int64]int{1: 1, 2: 2, 3: 1} {
		if got := e.rec.applyCount(seq); got != want {
			t.Errorf("operation: effect %d applied %d times, want %d", seq, got, want)
		}
	}
	// Both runs of effect 2 carried the token minted by the one Prepare.
	tokens := e.rec.tokensFor(2)
	if len(tokens) != 2 || tokens[0] != "token-2" || tokens[1] != "token-2" {
		t.Errorf("operation: effect 2 tokens = %v, want token-2 twice (journaled identity)", tokens)
	}
}

func TestRunCrashInUnrecordedApplyWindowRollBackLeaksEffect(t *testing.T) {
	e := newEnv(t, 3, RollBack)
	restore := setUnrecordedApplyFailpoint(t, e.op.ID(), 2)
	mustCrash(t, e.mgr, e.op)
	restore()

	if err := e.reopen(t, true).RecoverPending(context.Background()); err != nil {
		t.Fatalf("operation: recover: %v", err)
	}
	if state := opState(t, e.db, e.op.ID()); state != store.OpRolledBack {
		t.Errorf("operation: state after recovery = %s, want %s", state, store.OpRolledBack)
	}
	// Documented leak, asserted precisely: roll-back reverts only recorded
	// effects. Effect 2's side effect was performed (once, in the
	// unrecorded window) yet is never reverted and never re-applied.
	if got, want := e.rec.revertedSeqs(), []int64{1}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("operation: reverts = %v, want %v (effect 2 leaks un-reverted)", got, want)
	}
	if got := e.rec.applyCount(2); got != 1 {
		t.Errorf("operation: effect 2 applied %d times, want exactly 1 (performed in the window, never re-applied under roll-back)", got)
	}
	states := effectStates(t, e.db, e.op.ID())
	for seq := int64(1); seq <= 3; seq++ {
		if states[seq] != store.EffectReverted {
			t.Errorf("operation: effect %d state = %s, want %s (closed without Revert)", seq, states[seq], store.EffectReverted)
		}
	}
}
