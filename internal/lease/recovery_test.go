package lease

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// crashAtEveryBoundary drives AcquireAll to a crash at each journal boundary
// and proves roll-forward recovery converges to a fully activated work.
func TestAcquireAllCrashAtEveryBoundaryRollsForward(t *testing.T) {
	cases := []struct {
		name  string
		point string
		nth   int
	}{
		{"before any lease", "prepared", 1},
		{"after lease 1", "effect-applied", 1},
		{"after lease 2", "effect-applied", 2},
		{"after lease 3", "effect-applied", 3},
		{"after sentinel", "effect-applied", 4},
		{"after activation", "effect-applied", 5},
		{"after finalize", "finalized", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			restore := setFailpoint(t, tc.point, tc.nth)
			mustCrash(t, func() error {
				_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
				return err
			})
			restore()

			e.close(t)
			e.open(t)
			if err := e.mgr.Recover(context.Background()); err != nil {
				t.Fatalf("lease: recover: %v", err)
			}

			ops := acquireOpRows(t, e.db)
			if len(ops) != 1 || ops[0].State != store.OpFinalized {
				t.Fatalf("lease: acquire ops = %+v, want one finalized", ops)
			}
			for _, target := range e.allTargets() {
				if _, err := os.Stat(target.Path); err != nil {
					t.Errorf("lease: %s missing after recovery: %v", target.Path, err)
				}
			}
			sentinel, err := ReadSentinel(SentinelPath(e.dir, e.workID))
			if err != nil {
				t.Fatalf("lease: sentinel missing after recovery: %v", err)
			}
			if sentinel.OperationID != ops[0].ID {
				t.Errorf("lease: sentinel operation %s, want %s", sentinel.OperationID, ops[0].ID)
			}
			if state := workState(t, e.db, e.workID); state != "active" {
				t.Errorf("lease: work state = %q, want active", state)
			}
			if metaValue(t, e.db, "lease-op-applied:"+string(ops[0].ID)) != "1" {
				t.Error("lease: activation marker missing after recovery")
			}
		})
	}
}

// TestAcquireAllCrashInUnrecordedLeaseWindowConverges proves the idempotent
// re-apply contract on the lease effect itself: the crash leaves a lease
// file on disk whose row still reads pending, and roll-forward recovery must
// no-op on it rather than error on the O_EXCL collision.
func TestAcquireAllCrashInUnrecordedLeaseWindowConverges(t *testing.T) {
	e := newEnv(t)
	captured := captureUnrecordedCrash(t, "effect-applied-unrecorded:", 1)
	mustCrash(t, func() error {
		_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
		return err
	})
	captured.restore()

	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpPrepared {
		t.Fatalf("lease: acquire ops = %+v, want one prepared", ops)
	}
	if captured.id != ops[0].ID {
		t.Fatalf("lease: crashed op %s, want %s", captured.id, ops[0].ID)
	}

	e.close(t)
	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: recover: %v", err)
	}
	if ops := acquireOpRows(t, e.db); len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: acquire ops = %+v, want one finalized", ops)
	}
	for _, target := range e.allTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("lease: %s missing after recovery: %v", target.Path, err)
		}
	}
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active", state)
	}
}

// TestSentinelNeverRolledBackAfterCommitMarker crashes in the sentinel's
// unrecorded window: the commit marker file exists on disk while its journal
// row still reads pending. Recovery must finish the projection (roll
// forward), never roll the activation back — a roll-back decision here would
// hit the sentinel's defensive Revert error and fail the pass.
func TestSentinelNeverRolledBackAfterCommitMarker(t *testing.T) {
	e := newEnv(t)
	captured := captureUnrecordedCrash(t, "effect-applied-unrecorded:", 4) // sentinel is effect 4
	mustCrash(t, func() error {
		_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
		return err
	})
	captured.restore()

	// The commit marker is durable even though its row is not.
	if _, err := os.Stat(SentinelPath(e.dir, e.workID)); err != nil {
		t.Fatalf("lease: sentinel missing after unrecorded crash: %v", err)
	}
	// A foreign lease now occupies the last target — the tempting roll-back
	// case. The marker's presence must override it: recovery finishes the
	// projection forward and never undoes the activation.
	writeForeignLease(t, e.target(3).Path)

	e.close(t)
	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: recover: %v", err)
	}
	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: acquire ops = %+v, want one finalized (activation never rolled back)", ops)
	}
	if _, err := os.Stat(SentinelPath(e.dir, e.workID)); err != nil {
		t.Errorf("lease: sentinel removed by recovery: %v", err)
	}
	// The foreign file is never overwritten or removed; lease rows were
	// already applied, so recovery trusts the journal and leaves the
	// divergence on disk for the holder to observe.
	foreign, err := ReadLease(e.target(3).Path)
	if err != nil {
		t.Fatalf("lease: read foreign lease: %v", err)
	}
	if foreign.WorkspaceID == e.wsID {
		t.Errorf("lease: foreign lease at 3 was replaced by our content")
	}
	// The projection finished: the work is active, exactly as if the crash
	// had never happened.
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active (finish projection)", state)
	}
}

// TestAcquireAllCrashRollsBackWhenRemainingLeaseBlocked: crash after lease 1
// with a foreign lease blocking target 2. Recovery must roll back the
// token-matching lease it holds and leave the foreign lease untouched.
func TestAcquireAllCrashRollsBackWhenRemainingLeaseBlocked(t *testing.T) {
	e := newEnv(t)
	writeForeignLease(t, e.target(2).Path)
	restore := setFailpoint(t, "effect-applied", 1)
	mustCrash(t, func() error {
		_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
		return err
	})
	restore()

	e.close(t)
	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: recover: %v", err)
	}

	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpRolledBack {
		t.Fatalf("lease: acquire ops = %+v, want one rolled_back", ops)
	}
	if _, err := os.Stat(e.target(1).Path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: token-matching lease 1 not removed, err = %v", err)
	}
	if _, err := os.Stat(e.target(2).Path); err != nil {
		t.Errorf("lease: foreign lease 2 removed by rollback: %v", err)
	}
	if _, err := os.Stat(e.target(3).Path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: lease 3 present, err = %v", err)
	}
	if _, err := ReadSentinel(SentinelPath(e.dir, e.workID)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: sentinel exists after rollback, err = %v", err)
	}
	if state := workState(t, e.db, e.workID); state != "" {
		t.Errorf("lease: work state = %q, want no activation", state)
	}

	// Recovery is idempotent: a second pass changes nothing.
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: second recover: %v", err)
	}
	if ops := acquireOpRows(t, e.db); len(ops) != 1 || ops[0].State != store.OpRolledBack {
		t.Fatalf("lease: acquire ops after second recover = %+v", ops)
	}
}

// TestAcquireAllCrashBeforeAnyLeaseWithBlockedTargetRollsBack: nothing was
// applied, yet the blocked target must still route recovery to roll-back so
// the foreign owner keeps the member.
func TestAcquireAllCrashBeforeAnyLeaseWithBlockedTargetRollsBack(t *testing.T) {
	e := newEnv(t)
	writeForeignLease(t, e.target(2).Path)
	restore := setFailpoint(t, "prepared", 1)
	mustCrash(t, func() error {
		_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
		return err
	})
	restore()

	e.close(t)
	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: recover: %v", err)
	}
	if ops := acquireOpRows(t, e.db); len(ops) != 1 || ops[0].State != store.OpRolledBack {
		t.Fatalf("lease: acquire ops = %+v, want one rolled_back", ops)
	}
	if _, err := os.Stat(e.target(2).Path); err != nil {
		t.Errorf("lease: foreign lease removed, err = %v", err)
	}
}

// TestRecoveryCrashMidRollForwardConverges interrupts recovery itself at the
// same unrecorded boundary contract the journal guarantees.
func TestRecoveryCrashMidRollForwardConverges(t *testing.T) {
	e := newEnv(t)
	restore := setFailpoint(t, "effect-applied", 1)
	mustCrash(t, func() error {
		_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
		return err
	})
	restore()

	e.close(t)
	e.open(t)
	restore = setFailpoint(t, "effect-applied", 1)
	mustRecoverCrash(t, func() error { return e.mgr.Recover(context.Background()) })
	restore()

	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpPrepared {
		t.Fatalf("lease: acquire ops after interrupted recovery = %+v, want one prepared", ops)
	}
	e.close(t)
	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: re-recover: %v", err)
	}
	if ops := acquireOpRows(t, e.db); len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: acquire ops after re-recovery = %+v, want one finalized", ops)
	}
	for _, target := range e.allTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("lease: %s missing, err = %v", target.Path, err)
		}
	}
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active", state)
	}
}

// captureUnrecordedCrash installs a hook that panics the first time an
// unrecorded-apply boundary with the given prefix and seq suffix fires, and
// captures the operation id embedded in the boundary name. The captured id
// is written through the returned pointer, so the test sees it after the
// panic unwinds.
func captureUnrecordedCrash(t *testing.T, prefix string, seq int64) *capturedCrash {
	t.Helper()
	c := &capturedCrash{}
	restore := operation.SetFailpointHook(func(p string) {
		if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, ":"+strconv.FormatInt(seq, 10)) {
			c.id = identity.OperationID(strings.TrimSuffix(strings.TrimPrefix(p, prefix), ":"+strconv.FormatInt(seq, 10)))
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
	t.Cleanup(restore)
	c.restore = restore
	return c
}

type capturedCrash struct {
	id      identity.OperationID
	restore func()
}

// TestSubprocessCrashConvergesOnRecovery is the real-process-death proof:
// the child exits via os.Exit mid-acquisition (WAL frames uncheckpointed),
// and a fresh process recovers the journal to a fully activated work.
func TestSubprocessCrashConvergesOnRecovery(t *testing.T) {
	e := newEnv(t)
	e.close(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestLeaseCrashChild$")
	cmd.Env = append(os.Environ(),
		"LEASE_CRASH_CHILD=1",
		"LEASE_CRASH_DIR="+e.dir,
		"LEASE_CRASH_DB="+e.dbPath,
		"LEASE_CRASH_WS="+string(e.wsID),
		"LEASE_CRASH_WORK="+string(e.workID),
		"LEASE_CRASH_POINT=effect-applied",
		"LEASE_CRASH_NTH=2",
	)
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 73 {
		t.Fatalf("lease: child exit = %v, want simulated crash (code 73)", err)
	}

	e.open(t)
	if err := e.mgr.Recover(context.Background()); err != nil {
		t.Fatalf("lease: recover after subprocess crash: %v", err)
	}
	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: acquire ops = %+v, want one finalized", ops)
	}
	for _, target := range e.allTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("lease: %s missing after subprocess recovery: %v", target.Path, err)
		}
	}
	if _, err := ReadSentinel(SentinelPath(e.dir, e.workID)); err != nil {
		t.Errorf("lease: sentinel missing after subprocess recovery: %v", err)
	}
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active", state)
	}
}

// TestLeaseCrashChild is the subprocess helper: it acquires leases and dies
// with os.Exit at the boundary named by the environment.
func TestLeaseCrashChild(t *testing.T) {
	if os.Getenv("LEASE_CRASH_CHILD") != "1" {
		return
	}
	dir := os.Getenv("LEASE_CRASH_DIR")
	dbPath := os.Getenv("LEASE_CRASH_DB")
	point := os.Getenv("LEASE_CRASH_POINT")
	nth, err := strconv.Atoi(os.Getenv("LEASE_CRASH_NTH"))
	if err != nil {
		t.Fatalf("lease: child nth: %v", err)
	}

	db, err := store.Open(context.Background(), dbPath, store.OpenOptions{})
	if err != nil {
		t.Fatalf("lease: child open store: %v", err)
	}
	mgr := NewManager(db, operation.NewManager(db))

	count := 0
	operation.SetFailpointHook(func(p string) {
		if p != point {
			return
		}
		count++
		if count == nth {
			os.Exit(73)
		}
	})

	prov, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: child process: %v", err)
	}
	var targets []Target
	for _, name := range []string{"member-a", "member-b", "member-c"} {
		repoID, err := identity.NewRepositoryID()
		if err != nil {
			t.Fatalf("lease: child repository id: %v", err)
		}
		targets = append(targets, Target{
			RepositoryID: repoID,
			Path:         filepath.Join(dir, name, "lease.json"),
		})
	}
	if _, err := mgr.AcquireAll(context.Background(), AcquireRequest{
		WorkspaceID: identity.WorkspaceID(os.Getenv("LEASE_CRASH_WS")),
		WorkID:      identity.WorkID(os.Getenv("LEASE_CRASH_WORK")),
		Generation:  1,
		Provenance:  prov,
		ControlRoot: dir,
		Targets:     targets,
	}); err != nil {
		t.Fatalf("lease: child acquire: %v", err)
	}
}
