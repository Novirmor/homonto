package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// wsEnv wires one active workspace: control root with a committed config,
// member directories, a runtime database, and a lease manager holding every
// member's lease for one active work.
type wsEnv struct {
	wsRoot    string
	stateRoot string
	dbPath    string
	db        *store.DB
	ops       *operation.Manager
	lmg       *lease.Manager
	svc       *Service
	wsID      identity.WorkspaceID
	workID    identity.WorkID
	controlID identity.RepositoryID
	memberA   identity.RepositoryID
	memberB   identity.RepositoryID
	cfg       workspacecfg.Config
}

func newWSEnv(t *testing.T) *wsEnv {
	t.Helper()
	wsRoot := t.TempDir()
	stateRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wsRoot, ".homonto"), 0o755); err != nil {
		t.Fatalf("workspace: mkdir .homonto: %v", err)
	}
	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(wsRoot, name), 0o755); err != nil {
			t.Fatalf("workspace: mkdir %s: %v", name, err)
		}
	}
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("workspace: workspace id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("workspace: work id: %v", err)
	}
	controlID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: control id: %v", err)
	}
	memberA, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: member id: %v", err)
	}
	memberB, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: member id: %v", err)
	}
	e := &wsEnv{
		wsRoot: wsRoot, stateRoot: stateRoot,
		dbPath: filepath.Join(wsRoot, ".homonto", "runtime.db"),
		wsID:   wsID, workID: workID,
		controlID: controlID, memberA: memberA, memberB: memberB,
	}
	e.cfg = workspacecfg.Config{
		SchemaVersion: 1,
		Workspace:     workspacecfg.Workspace{ID: wsID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
		Members: []workspacecfg.Member{
			{ID: controlID, Path: ".", Kind: workspacecfg.KindNonGit},
			{ID: memberA, Path: "member-a", Kind: workspacecfg.KindNonGit},
			{ID: memberB, Path: "member-b", Kind: workspacecfg.KindNonGit},
		},
	}
	e.writeConfig(t, e.cfg)
	e.open(t)
	e.activate(t)
	return e
}

func (e *wsEnv) open(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), e.dbPath, store.OpenOptions{})
	if err != nil {
		t.Fatalf("workspace: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	e.ops = operation.NewManager(db)
	e.lmg = lease.NewManager(db, e.ops)
	e.svc = NewService(db, e.ops, e.stateRoot, nil)
}

// close releases the database handle so a reopen can simulate a new process
// against the same journal after a crash.
func (e *wsEnv) close(t *testing.T) {
	t.Helper()
	if e.db == nil {
		return
	}
	if err := e.db.Close(); err != nil {
		t.Fatalf("workspace: close store: %v", err)
	}
	e.db = nil
}

// setFailpoint installs a hook that panics (simulating process death) the
// nth time point is reached. The returned restore clears the hook.
func setFailpoint(t *testing.T, point string, nth int) (restore func()) {
	t.Helper()
	counts := map[string]int{}
	return operation.SetFailpointHook(func(p string) {
		if p != point {
			return
		}
		counts[p]++
		if counts[p] == nth {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// mustCrash runs run and fails the test unless the failpoint panicked.
func mustCrash(t *testing.T, run func() error) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := run(); err != nil {
			panic(fmt.Sprintf("returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("workspace: expected simulated crash at failpoint")
	}
}

func (e *wsEnv) writeConfig(t *testing.T, cfg workspacecfg.Config) {
	t.Helper()
	data, err := workspacecfg.Marshal(cfg)
	if err != nil {
		t.Fatalf("workspace: marshal config: %v", err)
	}
	if err := os.WriteFile(ConfigPath(e.wsRoot), data, 0o644); err != nil {
		t.Fatalf("workspace: write config: %v", err)
	}
}

// activate acquires leases for every configured member and leaves the work
// active with a sentinel in place.
func (e *wsEnv) activate(t *testing.T) {
	t.Helper()
	prov, err := lease.CurrentProcess()
	if err != nil {
		t.Fatalf("workspace: process: %v", err)
	}
	var targets []lease.Target
	for _, m := range e.cfg.Members {
		path, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, filepath.FromSlash(m.Path)))
		if err != nil {
			t.Fatalf("workspace: lease path: %v", err)
		}
		targets = append(targets, lease.Target{RepositoryID: m.ID, Path: path})
	}
	if _, err := e.lmg.AcquireAll(context.Background(), lease.AcquireRequest{
		WorkspaceID: e.wsID,
		WorkID:      e.workID,
		Generation:  1,
		Provenance:  prov,
		ControlRoot: e.wsRoot,
		Targets:     targets,
	}); err != nil {
		t.Fatalf("workspace: activate: %v", err)
	}
}

// readConfig decodes the on-disk config.
func (e *wsEnv) readConfig(t *testing.T) workspacecfg.Config {
	t.Helper()
	data, err := os.ReadFile(ConfigPath(e.wsRoot))
	if err != nil {
		t.Fatalf("workspace: read config: %v", err)
	}
	cfg, err := workspacecfg.Decode(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("workspace: decode config: %v", err)
	}
	return cfg
}

func (e *wsEnv) req(t *testing.T, oldCfg, newCfg workspacecfg.Config) RescanRequest {
	t.Helper()
	return RescanRequest{
		WorkspaceID:   e.wsID,
		WorkID:        e.workID,
		Generation:    1,
		ControlRoot:   e.wsRoot,
		WorkspaceRoot: e.wsRoot,
		OldConfig:     oldCfg,
		NewConfig:     newCfg,
	}
}

func (e *wsEnv) sentinel(t *testing.T) lease.SentinelContent {
	t.Helper()
	c, err := lease.ReadSentinel(lease.SentinelPath(e.wsRoot, e.workID))
	if err != nil {
		t.Fatalf("workspace: sentinel: %v", err)
	}
	return c
}

func (e *wsEnv) rescanOps(t *testing.T) map[string]int {
	t.Helper()
	counts := map[string]int{}
	ctx := context.Background()
	err := e.db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT state, COUNT(*) FROM operations WHERE kind=? GROUP BY state`, "lease.rescan")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state string
			var n int
			if err := rows.Scan(&state, &n); err != nil {
				return err
			}
			counts[state] = n
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("workspace: rescan ops: %v", err)
	}
	return counts
}

// addMember returns a copy of cfg with member added.
func (e *wsEnv) addMember(t *testing.T, cfg workspacecfg.Config, id identity.RepositoryID, path, kind string) workspacecfg.Config {
	t.Helper()
	dir := filepath.Join(e.wsRoot, path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("workspace: mkdir %s: %v", path, err)
	}
	if kind == string(workspacecfg.KindGit) {
		if err := gitx.Init(context.Background(), gitx.ExecRunner{}, dir); err != nil {
			t.Fatalf("workspace: git init %s: %v", path, err)
		}
	}
	out := cfg
	out.Members = append(append([]workspacecfg.Member(nil), cfg.Members...),
		workspacecfg.Member{ID: id, Path: path, Kind: workspacecfg.MemberKind(kind)})
	return out
}

// withoutMember returns a copy of cfg with the member removed.
func (e *wsEnv) withoutMember(cfg workspacecfg.Config, id identity.RepositoryID) workspacecfg.Config {
	out := cfg
	out.Members = nil
	for _, m := range cfg.Members {
		if m.ID != id {
			out.Members = append(out.Members, m)
		}
	}
	return out
}

func TestRescanActiveAddsMemberBeforeConfigActivation(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	regPath, err := registration.NonGitRegistrationPath(e.stateRoot, filepath.Join(e.wsRoot, "member-c"))
	if err != nil {
		t.Fatalf("workspace: registration path: %v", err)
	}
	leasePath, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, "member-c"))
	if err != nil {
		t.Fatalf("workspace: lease path: %v", err)
	}

	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)); err != nil {
		t.Fatalf("workspace: rescan add: %v", err)
	}

	reg, err := registration.Read(regPath)
	if err != nil {
		t.Fatalf("workspace: claim missing after add: %v", err)
	}
	if reg.WorkspaceID != e.wsID || reg.RepositoryID != newID {
		t.Errorf("workspace: claim = %+v, want workspace %s member %s", reg, e.wsID, newID)
	}
	if _, err := lease.ReadLease(leasePath); err != nil {
		t.Fatalf("workspace: lease missing after add: %v", err)
	}
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(newCfg) {
		t.Errorf("workspace: config on disk does not match the new membership: %+v", got.Members)
	}
	if v := e.sentinel(t).Version; v != 2 {
		t.Errorf("workspace: sentinel version = %d, want 2 after membership change", v)
	}
	if counts := e.rescanOps(t); counts["finalized"] != 1 {
		t.Errorf("workspace: rescan op outcomes = %v, want one finalized", counts)
	}
}

func TestRescanActiveAddRollsBackOnLeaseFailure(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	regPath, err := registration.NonGitRegistrationPath(e.stateRoot, filepath.Join(e.wsRoot, "member-c"))
	if err != nil {
		t.Fatalf("workspace: registration path: %v", err)
	}
	leasePath, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, "member-c"))
	if err != nil {
		t.Fatalf("workspace: lease path: %v", err)
	}
	// A foreign owner occupies the new member's lease slot before rescan.
	foreignWS, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("workspace: workspace id: %v", err)
	}
	foreignWork, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("workspace: work id: %v", err)
	}
	token, err := identity.NewToken()
	if err != nil {
		t.Fatalf("workspace: token: %v", err)
	}
	prov, err := lease.CurrentProcess()
	if err != nil {
		t.Fatalf("workspace: process: %v", err)
	}
	foreign := lease.LeaseContent{
		SchemaVersion: 1, WorkspaceID: foreignWS, RepositoryID: newID,
		WorkID: foreignWork, Generation: 1, Process: prov, RecoveryToken: token,
	}
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o755); err != nil {
		t.Fatalf("workspace: mkdir: %v", err)
	}
	if err := os.WriteFile(leasePath, mustMarshal(t, foreign), 0o600); err != nil {
		t.Fatalf("workspace: write foreign lease: %v", err)
	}

	err = e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg))
	if !errors.Is(err, lease.ErrLeaseConflict) {
		t.Fatalf("workspace: rescan add error = %v, want ErrLeaseConflict", err)
	}

	if _, rerr := registration.Read(regPath); !errors.Is(rerr, registration.ErrNotRegistered) {
		t.Errorf("workspace: claim survived rollback, err = %v", rerr)
	}
	foreignContent, rerr := lease.ReadLease(leasePath)
	if rerr != nil || foreignContent.WorkspaceID != foreignWS {
		t.Errorf("workspace: foreign lease disturbed by rollback: %+v, %v", foreignContent, rerr)
	}
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(e.cfg) {
		t.Errorf("workspace: config changed by failed rescan: %+v", got.Members)
	}
	if v := e.sentinel(t).Version; v != 1 {
		t.Errorf("workspace: sentinel version = %d, want unchanged 1", v)
	}
	if counts := e.rescanOps(t); counts["rolled_back"] != 1 {
		t.Errorf("workspace: rescan op outcomes = %v, want one rolled_back", counts)
	}
}

func TestRescanActiveRemoveBlockedWhileIntegrationsExist(t *testing.T) {
	e := newWSEnv(t)
	integrations := IntegrationsDir(e.wsRoot, e.workID, e.memberA)
	if err := os.MkdirAll(integrations, 0o755); err != nil {
		t.Fatalf("workspace: mkdir integrations: %v", err)
	}
	newCfg := e.withoutMember(e.cfg, e.memberA)

	err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg))
	if !errors.Is(err, ErrUnintegratedAssignments) {
		t.Fatalf("workspace: rescan remove error = %v, want ErrUnintegratedAssignments", err)
	}
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(e.cfg) {
		t.Errorf("workspace: config changed despite block: %+v", got.Members)
	}
	if v := e.sentinel(t).Version; v != 1 {
		t.Errorf("workspace: sentinel version = %d, want unchanged 1", v)
	}
	leasePath, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, "member-a"))
	if err != nil {
		t.Fatalf("workspace: lease path: %v", err)
	}
	if _, err := lease.ReadLease(leasePath); err != nil {
		t.Errorf("workspace: member-a lease disturbed by blocked rescan: %v", err)
	}
	if counts := e.rescanOps(t); len(counts) != 0 {
		t.Errorf("workspace: blocked rescan journaled an op: %v", counts)
	}
}

func TestRescanActiveRemovesMemberAndReleasesLeaseAfterCommit(t *testing.T) {
	e := newWSEnv(t)
	newCfg := e.withoutMember(e.cfg, e.memberB)
	leasePath, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, "member-b"))
	if err != nil {
		t.Fatalf("workspace: lease path: %v", err)
	}

	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)); err != nil {
		t.Fatalf("workspace: rescan remove: %v", err)
	}

	if got := e.readConfig(t); len(got.Members) != 2 {
		t.Errorf("workspace: config on disk = %+v, want member-b removed", got.Members)
	}
	if _, err := lease.ReadLease(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("workspace: member-b lease still present after removal, err = %v", err)
	}
	if v := e.sentinel(t).Version; v != 2 {
		t.Errorf("workspace: sentinel version = %d, want 2 after membership change", v)
	}
	if counts := e.rescanOps(t); counts["finalized"] != 1 {
		t.Errorf("workspace: rescan op outcomes = %v, want one finalized", counts)
	}
}

func TestRescanActiveMembershipFingerprintChanges(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	oldFP := workspacecfg.MembershipFingerprint(e.cfg)
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	newFP := workspacecfg.MembershipFingerprint(newCfg)
	if oldFP == newFP {
		t.Fatalf("workspace: membership fingerprints identical despite new member")
	}

	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)); err != nil {
		t.Fatalf("workspace: rescan add: %v", err)
	}
	if got := workspacecfg.MembershipFingerprint(e.readConfig(t)); got != newFP {
		t.Errorf("workspace: on-disk membership fingerprint = %s, want %s (evidence invalidation signal)", got, newFP)
	}
	if v := e.sentinel(t).Version; v != 2 {
		t.Errorf("workspace: sentinel version = %d, want 2", v)
	}
}

func TestRescanActiveAddsGitMember(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-d", string(workspacecfg.KindGit))

	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)); err != nil {
		t.Fatalf("workspace: rescan git add: %v", err)
	}

	repo, isGit, err := gitx.Inspect(context.Background(), gitx.ExecRunner{}, filepath.Join(e.wsRoot, "member-d"))
	if err != nil || !isGit {
		t.Fatalf("workspace: inspect member-d: %v, %v", isGit, err)
	}
	regPath := registration.GitRegistrationPath(repo.CommonDir)
	reg, err := registration.Read(regPath)
	if err != nil {
		t.Fatalf("workspace: git claim missing: %v", err)
	}
	if reg.Kind != workspacecfg.KindGit || reg.WorkspaceID != e.wsID {
		t.Errorf("workspace: git claim = %+v, want kind git workspace %s", reg, e.wsID)
	}
	if _, err := lease.ReadLease(registration.GitLeasePath(repo.CommonDir)); err != nil {
		t.Fatalf("workspace: git lease missing: %v", err)
	}
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(newCfg) {
		t.Errorf("workspace: config on disk lost the git kind: %+v", got.Members)
	}
}

func TestRescanActiveRequiresActiveWork(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	// No activation: remove the sentinel the setup acquired.
	if err := os.Remove(lease.SentinelPath(e.wsRoot, e.workID)); err != nil {
		t.Fatalf("workspace: remove sentinel: %v", err)
	}

	err = e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg))
	if !errors.Is(err, ErrNotActiveWork) {
		t.Fatalf("workspace: rescan without active work = %v, want ErrNotActiveWork", err)
	}
}

func TestRescanActiveNoChangeIsNoOp(t *testing.T) {
	e := newWSEnv(t)
	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, e.cfg)); err != nil {
		t.Fatalf("workspace: rescan no-op: %v", err)
	}
	if counts := e.rescanOps(t); len(counts) != 0 {
		t.Errorf("workspace: no-op rescan journaled an op: %v", counts)
	}
	if v := e.sentinel(t).Version; v != 1 {
		t.Errorf("workspace: sentinel version = %d, want unchanged 1", v)
	}
}

func mustMarshal(t *testing.T, c lease.LeaseContent) []byte {
	t.Helper()
	data, err := c.Marshal()
	if err != nil {
		t.Fatalf("workspace: marshal lease: %v", err)
	}
	return data
}

// nonGitLeasePath is the lease slot for a member directory under the env's
// state root.
func (e *wsEnv) nonGitLeasePath(t *testing.T, rel string) string {
	t.Helper()
	p, err := registration.NonGitLeasePath(e.stateRoot, filepath.Join(e.wsRoot, rel))
	if err != nil {
		t.Fatalf("workspace: lease path for %s: %v", rel, err)
	}
	return p
}

// leaseSet returns the sentinel's lease list keyed by repository id.
func leaseSet(t *testing.T, e *wsEnv) map[identity.RepositoryID]string {
	t.Helper()
	set := map[identity.RepositoryID]string{}
	for _, l := range e.sentinel(t).Leases {
		set[l.RepositoryID] = l.Path
	}
	return set
}

// assertSentinelMatchesDisk verifies the marker's lease list is exactly the
// set of leases present on disk: every listed path exists, and no listed
// path is missing.
func assertSentinelMatchesDisk(t *testing.T, e *wsEnv) {
	t.Helper()
	for _, l := range e.sentinel(t).Leases {
		if _, err := lease.ReadLease(l.Path); err != nil {
			t.Errorf("workspace: sentinel lists %s but no lease on disk: %v", l.Path, err)
		}
	}
}

// TestRescanActiveAddThenRemoveKeepsSentinelCurrent: a member added by one
// rescan must appear in the marker's lease list, so a second rescan can
// remove it — and the marker must then drop it again, always describing the
// leases actually held (ADR 0026).
func TestRescanActiveAddThenRemoveKeepsSentinelCurrent(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	withC := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	if err := e.svc.RescanActive(context.Background(), e.req(t, e.cfg, withC)); err != nil {
		t.Fatalf("workspace: rescan add: %v", err)
	}

	sent := e.sentinel(t)
	if sent.Version != 2 {
		t.Fatalf("workspace: sentinel version = %d, want 2 after add", sent.Version)
	}
	afterAdd := leaseSet(t, e)
	if _, ok := afterAdd[newID]; !ok {
		t.Fatalf("workspace: sentinel lease list %v does not include the added member %s", afterAdd, newID)
	}
	if afterAdd[newID] != e.nonGitLeasePath(t, "member-c") {
		t.Errorf("workspace: sentinel lease path for %s = %q, want %q",
			newID, afterAdd[newID], e.nonGitLeasePath(t, "member-c"))
	}
	assertSentinelMatchesDisk(t, e)

	withoutC := e.withoutMember(withC, newID)
	if err := e.svc.RescanActive(context.Background(), e.req(t, withC, withoutC)); err != nil {
		t.Fatalf("workspace: rescan remove of previously added member: %v", err)
	}

	sent = e.sentinel(t)
	if sent.Version != 3 {
		t.Fatalf("workspace: sentinel version = %d, want 3 after remove", sent.Version)
	}
	afterRemove := leaseSet(t, e)
	if _, ok := afterRemove[newID]; ok {
		t.Errorf("workspace: sentinel lease list %v still lists the removed member %s", afterRemove, newID)
	}
	if len(afterRemove) != 3 {
		t.Errorf("workspace: sentinel lease list %v, want the 3 remaining members", afterRemove)
	}
	if _, err := lease.ReadLease(e.nonGitLeasePath(t, "member-c")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("workspace: removed member's lease still present, err = %v", err)
	}
	assertSentinelMatchesDisk(t, e)
}

// TestRescanActiveRejectsStaleOldConfig: the operation must refuse to act on
// a base that is not the committed config on disk, before any effect runs —
// otherwise a never-claimed member could be activated by a stale delta.
func TestRescanActiveRejectsStaleOldConfig(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	// Stale base: claims member-a is absent although the committed config
	// lists it.
	stale := e.withoutMember(e.cfg, e.memberA)
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))

	err = e.svc.RescanActive(context.Background(), e.req(t, stale, newCfg))
	if !errors.Is(err, ErrConfigMismatch) {
		t.Fatalf("workspace: rescan with stale OldConfig = %v, want ErrConfigMismatch", err)
	}

	// No effects fired: the committed config is untouched, no claim or lease
	// was created, and nothing was journaled.
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(e.cfg) {
		t.Errorf("workspace: config changed by stale-base rescan: %+v", got.Members)
	}
	if _, err := lease.ReadLease(e.nonGitLeasePath(t, "member-c")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("workspace: lease created despite stale base, err = %v", err)
	}
	regPath, err := registration.NonGitRegistrationPath(e.stateRoot, filepath.Join(e.wsRoot, "member-c"))
	if err != nil {
		t.Fatalf("workspace: registration path: %v", err)
	}
	if _, err := registration.Read(regPath); !errors.Is(err, registration.ErrNotRegistered) {
		t.Errorf("workspace: claim created despite stale base, err = %v", err)
	}
	if counts := e.rescanOps(t); len(counts) != 0 {
		t.Errorf("workspace: stale-base rescan journaled an op: %v", counts)
	}
	if v := e.sentinel(t).Version; v != 1 {
		t.Errorf("workspace: sentinel version = %d, want unchanged 1", v)
	}
}

// TestRescanActiveRejectsGenerationMismatch: the request generation must
// match the active work's generation recorded in the marker; a stale
// projection is refused before any effect runs.
func TestRescanActiveRejectsGenerationMismatch(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	req := e.req(t, e.cfg, newCfg)
	req.Generation = 2

	err = e.svc.RescanActive(context.Background(), req)
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("workspace: rescan with stale generation = %v, want ErrGenerationMismatch", err)
	}
	if counts := e.rescanOps(t); len(counts) != 0 {
		t.Errorf("workspace: stale-generation rescan journaled an op: %v", counts)
	}
	if v := e.sentinel(t).Version; v != 1 {
		t.Errorf("workspace: sentinel version = %d, want unchanged 1", v)
	}
}

// TestRescanActiveCrashAfterConfigWriteRollsForward: the process dies after
// the config activation effect committed but before the marker bump. Crash
// recovery must finish the projection forward: the marker is bumped with the
// updated lease list and the operation finalizes.
func TestRescanActiveCrashAfterConfigWriteRollsForward(t *testing.T) {
	e := newWSEnv(t)
	newID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("workspace: repository id: %v", err)
	}
	newCfg := e.addMember(t, e.cfg, newID, "member-c", string(workspacecfg.KindNonGit))
	// Effects: claim, lease, config, bump. Crash after the config effect.
	restore := setFailpoint(t, "effect-applied", 3)
	mustCrash(t, func() error { return e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)) })
	restore()

	// The config activation committed before the crash.
	if got := e.readConfig(t); workspacecfg.MembershipFingerprint(got) != workspacecfg.MembershipFingerprint(newCfg) {
		t.Fatalf("workspace: config not activated before crash: %+v", got.Members)
	}
	if counts := e.rescanOps(t); counts["prepared"] != 1 {
		t.Fatalf("workspace: rescan ops = %v, want one prepared", counts)
	}

	e.close(t)
	e.open(t)
	if err := e.svc.Recover(context.Background()); err != nil {
		t.Fatalf("workspace: recover after crash: %v", err)
	}

	if counts := e.rescanOps(t); counts["finalized"] != 1 {
		t.Fatalf("workspace: rescan ops = %v, want one finalized", counts)
	}
	sent := e.sentinel(t)
	if sent.Version != 2 {
		t.Errorf("workspace: sentinel version = %d, want 2 after recovery", sent.Version)
	}
	if _, ok := leaseSet(t, e)[newID]; !ok {
		t.Errorf("workspace: sentinel lease list %v misses the added member %s", leaseSet(t, e), newID)
	}
	if _, err := lease.ReadLease(e.nonGitLeasePath(t, "member-c")); err != nil {
		t.Errorf("workspace: added member's lease missing after recovery: %v", err)
	}
	assertSentinelMatchesDisk(t, e)
}

// TestRescanActiveCrashMidRemovalRollsForward: the process dies after the
// removal's release effect committed but before the membership commit
// (finalize). Crash recovery must converge forward: the marker keeps its
// updated lease list and the operation finalizes with the removed member's
// lease gone.
func TestRescanActiveCrashMidRemovalRollsForward(t *testing.T) {
	e := newWSEnv(t)
	newCfg := e.withoutMember(e.cfg, e.memberB)
	// Effects: config, bump, release. Crash after the release effect.
	restore := setFailpoint(t, "effect-applied", 3)
	mustCrash(t, func() error { return e.svc.RescanActive(context.Background(), e.req(t, e.cfg, newCfg)) })
	restore()

	// The release committed before the crash.
	if _, err := lease.ReadLease(e.nonGitLeasePath(t, "member-b")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace: member-b lease still present after crash, err = %v", err)
	}
	if counts := e.rescanOps(t); counts["prepared"] != 1 {
		t.Fatalf("workspace: rescan ops = %v, want one prepared", counts)
	}

	e.close(t)
	e.open(t)
	if err := e.svc.Recover(context.Background()); err != nil {
		t.Fatalf("workspace: recover after crash: %v", err)
	}

	if counts := e.rescanOps(t); counts["finalized"] != 1 {
		t.Fatalf("workspace: rescan ops = %v, want one finalized", counts)
	}
	sent := e.sentinel(t)
	if sent.Version != 2 {
		t.Errorf("workspace: sentinel version = %d, want 2 after recovery", sent.Version)
	}
	if _, ok := leaseSet(t, e)[e.memberB]; ok {
		t.Errorf("workspace: sentinel lease list %v still lists the removed member %s", leaseSet(t, e), e.memberB)
	}
	if _, err := lease.ReadLease(e.nonGitLeasePath(t, "member-b")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("workspace: removed member's lease still present after recovery, err = %v", err)
	}
	assertSentinelMatchesDisk(t, e)
}
