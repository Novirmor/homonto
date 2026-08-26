package lease

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

func TestAcquireAllCreatesLeasesInStableOrderAndActivates(t *testing.T) {
	e := newEnv(t)
	leases, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
	if err != nil {
		t.Fatalf("lease: acquire: %v", err)
	}

	sorted := append([]Target(nil), e.allTargets()...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].RepositoryID < sorted[j].RepositoryID })
	if len(leases) != len(sorted) {
		t.Fatalf("lease: got %d leases, want %d", len(leases), len(sorted))
	}
	for i, want := range sorted {
		if leases[i].Path != want.Path || leases[i].Content.RepositoryID != want.RepositoryID {
			t.Errorf("lease: lease %d = %s (%s), want %s in stable repository-id order",
				i, leases[i].Path, leases[i].Content.RepositoryID, want.Path)
		}
		if leases[i].OpID == "" || leases[i].Seq != int64(i+1) {
			t.Errorf("lease: lease %d journal linkage = op %s seq %d", i, leases[i].OpID, leases[i].Seq)
		}
	}

	for _, l := range leases {
		info, err := os.Stat(l.Path)
		if err != nil {
			t.Fatalf("lease: stat %s: %v", l.Path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("lease: %s mode = %o, want 600", l.Path, perm)
		}
		content, err := ReadLease(l.Path)
		if err != nil {
			t.Fatalf("lease: read %s: %v", l.Path, err)
		}
		if content != l.Content {
			t.Errorf("lease: %s content = %+v, want %+v", l.Path, content, l.Content)
		}
	}

	sentinel, err := ReadSentinel(SentinelPath(e.dir, e.workID))
	if err != nil {
		t.Fatalf("lease: read sentinel: %v", err)
	}
	if sentinel.WorkspaceID != e.wsID || sentinel.WorkID != e.workID || sentinel.Generation != 1 || sentinel.Version != 1 {
		t.Errorf("lease: sentinel = %+v, want workspace %s work %s generation 1 version 1", sentinel, e.wsID, e.workID)
	}
	if sentinel.OperationID != leases[0].OpID {
		t.Errorf("lease: sentinel operation %s, want %s", sentinel.OperationID, leases[0].OpID)
	}
	if len(sentinel.Leases) != len(sorted) {
		t.Fatalf("lease: sentinel lists %d leases, want %d", len(sentinel.Leases), len(sorted))
	}
	byID := map[identity.RepositoryID]string{}
	for _, l := range sentinel.Leases {
		byID[l.RepositoryID] = l.Path
	}
	for _, want := range sorted {
		if byID[want.RepositoryID] != want.Path {
			t.Errorf("lease: sentinel lease for %s = %q, want %q", want.RepositoryID, byID[want.RepositoryID], want.Path)
		}
	}

	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: acquire ops = %+v, want one finalized", ops)
	}
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active", state)
	}
	if metaValue(t, e.db, "lease-op-applied:"+string(leases[0].OpID)) != "1" {
		t.Error("lease: activation marker missing from meta")
	}
}

func TestAcquireAllRejectsInvalidRequests(t *testing.T) {
	e := newEnv(t)
	req := e.req(t, e.allTargets())
	cases := []struct {
		name   string
		mutate func(*AcquireRequest)
	}{
		{"bad workspace id", func(r *AcquireRequest) { r.WorkspaceID = "x" }},
		{"zero generation", func(r *AcquireRequest) { r.Generation = 0 }},
		{"no targets", func(r *AcquireRequest) { r.Targets = nil }},
		{"duplicate repository id", func(r *AcquireRequest) { r.Targets[0] = r.Targets[1] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := req
			tc.mutate(&r)
			if _, err := e.mgr.AcquireAll(context.Background(), r); !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("lease: AcquireAll error = %v, want ErrInvalidRequest", err)
			}
			if got := opCountByKind(t, e.db, "lease.acquire"); len(got) != 0 {
				t.Errorf("lease: invalid request journaled ops: %v", got)
			}
		})
	}
}

func TestAcquireAllConflictingLeaseFailsAndCleansUp(t *testing.T) {
	e := newEnv(t)
	writeForeignLease(t, e.target(2).Path)

	_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("lease: AcquireAll error = %v, want ErrLeaseConflict", err)
	}

	// Only token-matching leases are cleaned up: lease 1 was acquired and
	// removed again, the foreign lease at 2 is untouched, lease 3 never ran.
	if _, err := os.Stat(e.target(1).Path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: lease 1 still present after rollback, err = %v", err)
	}
	if _, err := os.Stat(e.target(2).Path); err != nil {
		t.Errorf("lease: foreign lease 2 removed by rollback: %v", err)
	}
	if _, err := os.Stat(e.target(3).Path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: lease 3 present after failure, err = %v", err)
	}
	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpRolledBack {
		t.Fatalf("lease: acquire ops = %+v, want one rolled_back", ops)
	}
	if _, err := ReadSentinel(SentinelPath(e.dir, e.workID)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lease: sentinel exists after failed acquire, err = %v", err)
	}
	if state := workState(t, e.db, e.workID); state != "" {
		t.Errorf("lease: work state = %q, want no activation", state)
	}
}

func TestAcquireAllStaleOwnerRefusalNamesOtherWorkspace(t *testing.T) {
	e := newEnv(t)
	writeForeignLease(t, e.target(1).Path)

	_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("lease: AcquireAll error = %v, want ErrLeaseConflict", err)
	}
	acquireErr := err
	foreign, rerr := ReadLease(e.target(1).Path)
	if rerr != nil {
		t.Fatalf("lease: read foreign lease: %v", rerr)
	}
	if !strings.Contains(acquireErr.Error(), string(foreign.WorkspaceID)) {
		t.Errorf("lease: error %q does not name the holding workspace %s", acquireErr, foreign.WorkspaceID)
	}
}

func TestAcquireAllConcurrentSameTargetsOneWinner(t *testing.T) {
	e := newEnv(t)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.mgr.AcquireAll(context.Background(), e.req(t, e.allTargets()))
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("lease: %d concurrent acquisitions won, want exactly 1", winners)
	}

	for _, target := range e.allTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("lease: winner lease at %s missing: %v", target.Path, err)
		}
	}
	if _, err := ReadSentinel(SentinelPath(e.dir, e.workID)); err != nil {
		t.Errorf("lease: sentinel missing after concurrent acquire: %v", err)
	}
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want active", state)
	}
	counts := opCountByKind(t, e.db, "lease.acquire")
	if counts[store.OpFinalized] != 1 || counts[store.OpRolledBack] != 1 {
		t.Errorf("lease: acquire op outcomes = %v, want one finalized and one rolled_back", counts)
	}
}

func TestReleaseAllRemovesLeasesAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	leases := e.acquire(t, e.allTargets())
	if err := e.mgr.ReleaseAll(context.Background(), leases); err != nil {
		t.Fatalf("lease: release: %v", err)
	}
	for _, l := range leases {
		if _, err := os.Stat(l.Path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("lease: %s still present after release, err = %v", l.Path, err)
		}
	}
	if err := e.mgr.ReleaseAll(context.Background(), leases); err != nil {
		t.Fatalf("lease: second release: %v", err)
	}
	counts := opCountByKind(t, e.db, "lease.release")
	if counts[store.OpFinalized] != 2 {
		t.Errorf("lease: release op outcomes = %v, want two finalized", counts)
	}
	// The work stays active until archive; release alone must not deactivate.
	if state := workState(t, e.db, e.workID); state != "active" {
		t.Errorf("lease: work state = %q, want still active", state)
	}
}

func TestReleaseAllMissingLeaseIsNoOp(t *testing.T) {
	e := newEnv(t)
	leases := e.acquire(t, e.allTargets())
	if err := os.Remove(leases[1].Path); err != nil {
		t.Fatalf("lease: remove: %v", err)
	}
	if err := e.mgr.ReleaseAll(context.Background(), leases); err != nil {
		t.Fatalf("lease: release with missing lease: %v", err)
	}
}

func TestReleaseAllRefusesForeignLease(t *testing.T) {
	e := newEnv(t)
	leases := e.acquire(t, e.allTargets())
	writeForeignLease(t, leases[0].Path)

	err := e.mgr.ReleaseAll(context.Background(), leases)
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("lease: ReleaseAll error = %v, want ErrLeaseConflict", err)
	}
	// All-or-none: the foreign file is untouched and the removal effects
	// that already ran were rolled back, so every lease is still present.
	for _, l := range leases {
		if _, serr := os.Stat(l.Path); serr != nil {
			t.Errorf("lease: %s removed by failed release: %v", l.Path, serr)
		}
	}
	foreign, rerr := ReadLease(leases[0].Path)
	if rerr != nil || foreign.WorkspaceID == e.wsID {
		t.Errorf("lease: foreign lease disturbed by release: %+v, %v", foreign, rerr)
	}
	counts := opCountByKind(t, e.db, "lease.release")
	if counts[store.OpRolledBack] != 1 {
		t.Errorf("lease: release op outcomes = %v, want one rolled_back", counts)
	}
}

// TestDeadProcessIsDiagnosticOnly pins the liveness contract: a lease whose
// holder process is dead is still valid (PID liveness is diagnostic, never a
// validation or takeover criterion), and a dead-pid lease still blocks
// another workspace's acquisition — no timeout-based or liveness-based
// reclamation exists.
func TestDeadProcessIsDiagnosticOnly(t *testing.T) {
	e := newEnv(t)
	dead := startExitChild(t)
	if dead.Alive() {
		t.Fatalf("lease: fixture pid %d unexpectedly alive", dead.PID)
	}
	req := e.req(t, e.allTargets())
	req.Provenance = dead

	leases, err := e.mgr.AcquireAll(context.Background(), req)
	if err != nil {
		t.Fatalf("lease: acquire with dead-pid provenance: %v", err)
	}
	for _, l := range leases {
		if l.Content.Process.Alive() {
			t.Errorf("lease: %s process reported alive despite dead pid %d", l.Path, l.Content.Process.PID)
		}
	}

	// Takeover is still refused even though the holder is dead: another
	// workspace acquiring the same targets fails, and the dead-pid leases
	// are untouched.
	foreignReq := req
	otherWS, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}
	foreignReq.WorkspaceID = otherWS
	if _, err := e.mgr.AcquireAll(context.Background(), foreignReq); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("lease: takeover of dead-pid lease = %v, want ErrLeaseConflict", err)
	}
	for _, l := range leases {
		if _, err := ReadLease(l.Path); err != nil {
			t.Errorf("lease: dead-pid lease %s disturbed by refused takeover: %v", l.Path, err)
		}
	}
}

// TestRecoverOneScopedCleanup proves the in-process failure cleanup targets
// only the failed operation, leaving a crashed sibling untouched — the
// contract AcquireAll's rollback path depends on when two acquisitions run
// concurrently.
func TestRecoverOneScopedCleanup(t *testing.T) {
	e := newEnv(t)
	other, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}

	// B crashes after its first lease; A fails at the same boundary.
	reqA := e.req(t, e.allTargets())
	reqB := reqA
	reqB.WorkspaceID = other

	restore := setFailpoint(t, "effect-applied", 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { recover() }() // B's simulated crash
		_, _ = e.mgr.AcquireAll(context.Background(), reqB)
	}()
	wg.Wait()
	restore()

	ops := acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpPrepared {
		t.Fatalf("lease: sibling ops = %+v, want one prepared", ops)
	}
	opIDB := ops[0].ID

	// A's in-process failure must not touch B: recover B only.
	if err := e.ops.RecoverOne(context.Background(), opIDB); err != nil {
		t.Fatalf("lease: RecoverOne: %v", err)
	}
	ops = acquireOpRows(t, e.db)
	if len(ops) != 1 || ops[0].State != store.OpFinalized {
		t.Fatalf("lease: ops after RecoverOne = %+v, want only B finalized", ops)
	}
}
