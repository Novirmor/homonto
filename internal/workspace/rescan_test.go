package workspace

import (
	"context"
	"errors"
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
