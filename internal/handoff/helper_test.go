package handoff

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// machine is one fully set-up homonto host: a git control repository with a
// committed bootstrap, a committed config, two non-git members, a runtime
// database holding an active work's leases (generation 1) and sentinel, and
// a local checkpoint on disk.
type machine struct {
	t         *testing.T
	root      string
	stateRoot string
	runner    gitx.Runner

	wsID      identity.WorkspaceID
	workID    identity.WorkID
	controlID identity.RepositoryID
	memberA   identity.RepositoryID
	memberB   identity.RepositoryID
	cfg       workspacecfg.Config
	cp        checkpoint.Checkpoint

	db  *store.DB
	ops *operation.Manager
	lmg *lease.Manager
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	root := t.TempDir()
	stateRoot := t.TempDir()
	runner := gitx.ExecRunner{}
	ctx := context.Background()

	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("handoff: mkdir %s: %v", name, err)
		}
	}
	members := []workspace.Candidate{
		{Path: filepath.Join(root, "member-a"), Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
		{Path: filepath.Join(root, "member-b"), Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
	}
	if _, err := workspace.CreateControlRepository(ctx, root, members, runner); err != nil {
		t.Fatalf("handoff: create control: %v", err)
	}

	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("handoff: workspace id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("handoff: work id: %v", err)
	}
	controlID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: control id: %v", err)
	}
	memberA, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: member a id: %v", err)
	}
	memberB, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: member b id: %v", err)
	}

	cfg := workspacecfg.Config{
		SchemaVersion: 1,
		Workspace:     workspacecfg.Workspace{ID: wsID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
		Members: []workspacecfg.Member{
			{ID: controlID, Path: ".", Kind: workspacecfg.KindGit},
			{ID: memberA, Path: "member-a", Kind: workspacecfg.KindNonGit},
			{ID: memberB, Path: "member-b", Kind: workspacecfg.KindNonGit},
		},
	}
	m := &machine{
		t: t, root: root, stateRoot: stateRoot, runner: runner,
		wsID: wsID, workID: workID, controlID: controlID, memberA: memberA, memberB: memberB,
		cfg: cfg,
	}
	m.writeConfig(t, cfg)
	m.open(t)
	m.activate(t)
	m.cp = m.writeCheckpoint(t, checkpoint.HandoffLocal)
	m.cleanup()
	fixtureMemberA, fixtureMemberB = memberA, memberB
	return m
}

func (m *machine) open(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), RuntimeDBPath(m.root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open store: %v", err)
	}
	m.db = db
	m.ops = operation.NewManager(db)
	m.lmg = lease.NewManager(db, m.ops)
}

func (m *machine) close(t *testing.T) {
	t.Helper()
	if m.db == nil {
		return
	}
	if err := m.db.Close(); err != nil {
		t.Fatalf("handoff: close store: %v", err)
	}
	m.db, m.ops, m.lmg = nil, nil, nil
}

// cleanup registers deferred closing of the machine's database.
func (m *machine) cleanup() {
	m.t.Cleanup(func() {
		if m.db != nil {
			_ = m.db.Close()
			m.db = nil
		}
	})
}

func (m *machine) writeConfig(t *testing.T, cfg workspacecfg.Config) {
	t.Helper()
	data, err := workspacecfg.Marshal(cfg)
	if err != nil {
		t.Fatalf("handoff: marshal config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(workspace.ConfigPath(m.root)), 0o755); err != nil {
		t.Fatalf("handoff: mkdir .homonto: %v", err)
	}
	if err := os.WriteFile(workspace.ConfigPath(m.root), data, 0o644); err != nil {
		t.Fatalf("handoff: write config: %v", err)
	}
}

// activate acquires every member's lease at generation 1, leaving the
// sentinel in place — the fixture's "active work" state.
func (m *machine) activate(t *testing.T) {
	t.Helper()
	prov, err := lease.CurrentProcess()
	if err != nil {
		t.Fatalf("handoff: process: %v", err)
	}
	if _, err := m.lmg.AcquireAll(context.Background(), lease.AcquireRequest{
		WorkspaceID: m.wsID,
		WorkID:      m.workID,
		Generation:  1,
		Provenance:  prov,
		ControlRoot: m.root,
		WorkKind:    "task",
		Title:       "test-work",
		Targets:     m.leaseTargets(),
	}); err != nil {
		t.Fatalf("handoff: activate: %v", err)
	}
}

// leaseTargets returns one target per configured member, mirroring the
// production slot layout (git common dir / state-root slots).
func (m *machine) leaseTargets() []lease.Target {
	ctx := context.Background()
	var targets []lease.Target
	for _, mem := range m.cfg.Members {
		targets = append(targets, lease.Target{RepositoryID: mem.ID, Path: m.leasePath(ctx, mem)})
	}
	return targets
}

func (m *machine) leasePath(ctx context.Context, mem workspacecfg.Member) string {
	root := filepath.Join(m.root, filepath.FromSlash(mem.Path))
	if mem.Kind == workspacecfg.KindGit {
		repo, isGit, err := gitx.Inspect(ctx, m.runner, root)
		if err != nil || !isGit {
			m.t.Fatalf("handoff: inspect control %s: %v", root, err)
		}
		return registration.GitLeasePath(repo.CommonDir)
	}
	path, err := registration.NonGitLeasePath(m.stateRoot, root)
	if err != nil {
		m.t.Fatalf("handoff: lease path of %s: %v", root, err)
	}
	return path
}

// writeCheckpoint writes a checkpoint with the requested handoff state at
// generation 1 and returns it.
func (m *machine) writeCheckpoint(t *testing.T, state checkpoint.HandoffState) checkpoint.Checkpoint {
	t.Helper()
	ctx := context.Background()
	head, err := m.runner.Run(ctx, m.root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("handoff: head: %v", err)
	}
	commit := strings.TrimSpace(head)
	fp, err := workspacecfg.Fingerprint(m.cfg)
	if err != nil {
		t.Fatalf("handoff: fingerprint config: %v", err)
	}
	cp := checkpoint.Checkpoint{
		SchemaVersion:     checkpoint.CurrentSchemaVersion,
		WorkspaceID:       m.wsID,
		ConfigFingerprint: fp,
		Work: &checkpoint.Work{
			ID:         m.workID,
			Name:       "test-work",
			Workflow:   workspacecfg.WorkflowTask,
			Path:       "docs/homono/changes/test-work",
			Phase:      "build",
			Generation: 1,
		},
		Members: []checkpoint.Member{
			{
				ID: m.controlID, Kind: workspacecfg.KindGit,
				BaseBranch: "main", BaseCommit: commit,
				IntegrationBranch: "main", IntegrationCommit: commit,
				SourceFingerprint: fingerprintOf("control"),
			},
			{ID: m.memberA, Kind: workspacecfg.KindNonGit, SourceFingerprint: fingerprintOf("member-a")},
			{ID: m.memberB, Kind: workspacecfg.KindNonGit, SourceFingerprint: fingerprintOf("member-b")},
		},
		UnresolvedGates: []string{"verify"},
		Next:            &checkpoint.Next{Summary: "continue build"},
		Handoff:         checkpoint.Handoff{State: state, Generation: 1},
	}
	if err := checkpoint.Validate(cp, m.cfg); err != nil {
		t.Fatalf("handoff: fixture checkpoint invalid: %v", err)
	}
	root, err := securefs.OpenRoot(filepath.Join(m.root, ".homonto"))
	if err != nil {
		t.Fatalf("handoff: open checkpoint root: %v", err)
	}
	defer root.Close()
	st, err := checkpoint.NewStore(root, "checkpoint.json")
	if err != nil {
		t.Fatalf("handoff: checkpoint store: %v", err)
	}
	if _, err := st.Write(cp); err != nil {
		t.Fatalf("handoff: write checkpoint: %v", err)
	}
	return cp
}

func fingerprintOf(name string) fingerprint.Digest {
	return fingerprint.Bytes("member", []byte(name))
}

// prepare runs PreparePortable on this machine.
func (m *machine) prepare() error {
	return PreparePortable(context.Background(), PortableRequest{
		WorkspaceID: m.wsID,
		WorkID:      m.workID,
		ControlRoot: m.root,
		Git:         m.runner,
	})
}

// readCP loads the on-disk checkpoint of root.
func readCP(t *testing.T, root string) checkpoint.Checkpoint {
	t.Helper()
	cp, _, err := checkpoint.Load(CheckpointPath(root))
	if err != nil {
		t.Fatalf("handoff: read checkpoint: %v", err)
	}
	return cp
}

// headSubject returns HEAD's commit subject ("" when no commits).
func headSubject(t *testing.T, runner gitx.Runner, root string) string {
	t.Helper()
	out, err := runner.Run(context.Background(), root, "log", "-1", "--pretty=%s")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// gitShow returns a tracked file's bytes at a revision.
func gitShow(t *testing.T, runner gitx.Runner, root, ref string) []byte {
	t.Helper()
	out, err := runner.Run(context.Background(), root, "show", ref)
	if err != nil {
		t.Fatalf("handoff: git show %s: %v", ref, err)
	}
	return []byte(out)
}

// cloneControl clones src into a fresh destination directory and returns it.
func cloneControl(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "ws")
	parent := filepath.Dir(dst)
	if _, err := (gitx.ExecRunner{}).Run(context.Background(), parent, "clone", src, dst); err != nil {
		t.Fatalf("handoff: clone %s: %v", src, err)
	}
	return dst
}

// newAttachedHost prepares machine m for handoff, performs the portable
// handoff, and returns the new host's control root and state root (a fresh
// clone plus fresh member directories).
func newAttachedHost(t *testing.T, m *machine) (root, stateRoot string, transferID identity.Token) {
	t.Helper()
	m.close(t)
	if err := m.prepare(); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	cp := readCP(t, m.root)
	if cp.Handoff.State != checkpoint.HandoffTransferable {
		t.Fatalf("handoff: checkpoint not transferable after prepare: %s", cp.Handoff.State)
	}
	root = cloneControl(t, m.root)
	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("handoff: mkdir %s: %v", name, err)
		}
	}
	return root, t.TempDir(), cp.Handoff.TransferID
}

// attachReq builds the confirmed AttachRequest for a cloned host.
func attachReq(root, stateRoot string, force bool) AttachRequest {
	return AttachRequest{
		ControlRoot: root,
		Mappings: []ConfirmedMapping{
			{RepositoryID: fixtureMemberA, Path: filepath.Join(root, "member-a")},
			{RepositoryID: fixtureMemberB, Path: filepath.Join(root, "member-b")},
		},
		Force:     force,
		StateRoot: stateRoot,
		Git:       gitx.ExecRunner{},
	}
}

// fixture ids shared through attachReq; set by newMachine via package vars.
var (
	fixtureMemberA identity.RepositoryID
	fixtureMemberB identity.RepositoryID
)

// memberPathsOnHost returns the canonical member roots of a cloned host.
func memberPathsOnHost(root string) map[identity.RepositoryID]string {
	return map[identity.RepositoryID]string{
		fixtureMemberA: filepath.Join(root, "member-a"),
		fixtureMemberB: filepath.Join(root, "member-b"),
	}
}

// assertConvergedAttach verifies the full post-attach state of a host.
func assertConvergedAttach(t *testing.T, root, stateRoot string, wsID identity.WorkspaceID, workID identity.WorkID, gen uint64, transferID identity.Token) {
	t.Helper()
	ctx := context.Background()
	runner := gitx.ExecRunner{}

	cp := readCP(t, root)
	if cp.Handoff.State != checkpoint.HandoffConsumed || cp.Handoff.Generation != gen {
		t.Errorf("handoff: checkpoint handoff = %+v, want consumed@%d", cp.Handoff, gen)
	}
	if transferID != "" && cp.Handoff.TransferID != transferID {
		t.Errorf("handoff: transfer id = %s, want %s", cp.Handoff.TransferID, transferID)
	}

	// Registrations: control inside the git common dir, members under the
	// new state root.
	regPaths := []string{filepath.Join(root, ".git", "homonto", "registration.json")}
	for _, p := range memberPathsOnHost(root) {
		reg, err := registration.NonGitRegistrationPath(stateRoot, p)
		if err != nil {
			t.Fatalf("handoff: registration path: %v", err)
		}
		regPaths = append(regPaths, reg)
	}
	for i, p := range regPaths {
		reg, err := registration.Read(p)
		if err != nil {
			t.Errorf("handoff: registration %d at %s: %v", i, p, err)
			continue
		}
		if reg.WorkspaceID != wsID || reg.ControlRoot != root {
			t.Errorf("handoff: registration %d = %+v, want workspace %s control %s", i, reg, wsID, root)
		}
	}

	// Leases at the checkpoint generation, listed by a fresh sentinel.
	leasePaths := []string{filepath.Join(root, ".git", "homonto", "lease.json")}
	for _, p := range memberPathsOnHost(root) {
		lp, err := registration.NonGitLeasePath(stateRoot, p)
		if err != nil {
			t.Fatalf("handoff: lease path: %v", err)
		}
		leasePaths = append(leasePaths, lp)
	}
	for _, p := range leasePaths {
		content, err := lease.ReadLease(p)
		if err != nil {
			t.Errorf("handoff: lease %s: %v", p, err)
			continue
		}
		if content.WorkspaceID != wsID || content.WorkID != workID || content.Generation != gen {
			t.Errorf("handoff: lease %s = ws %s work %s gen %d, want gen %d", p, content.WorkspaceID, content.WorkID, content.Generation, gen)
		}
	}
	sentinel, err := lease.ReadSentinel(lease.SentinelPath(root, workID))
	if err != nil {
		t.Fatalf("handoff: sentinel: %v", err)
	}
	if sentinel.Generation != gen || len(sentinel.Leases) != 3 {
		t.Errorf("handoff: sentinel = gen %d leases %d, want gen %d leases 3", sentinel.Generation, len(sentinel.Leases), gen)
	}

	// Runtime rebuild: works, members, facts, meta, journal terminal.
	db, err := store.Open(ctx, RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime: %v", err)
	}
	defer func() { _ = db.Close() }()

	var workState, workKind, workTitle string
	var memberCount, factCount int
	var runtimeKey string
	err = db.View(ctx, func(tx *store.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT state, kind, title FROM works WHERE id=?`, string(workID)).
			Scan(&workState, &workKind, &workTitle); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM members`).Scan(&memberCount); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM facts WHERE subject=? AND predicate='source_fingerprint'`, string(wsID)).Scan(&factCount); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRuntimeKey).Scan(&runtimeKey)
	})
	if err != nil {
		t.Fatalf("handoff: runtime rows: %v", err)
	}
	if workState != "active" || workKind != "task" || workTitle != "test-work" {
		t.Errorf("handoff: works row = %s/%s/%s, want active/task/test-work", workState, workKind, workTitle)
	}
	if memberCount != 3 {
		t.Errorf("handoff: members rows = %d, want 3", memberCount)
	}
	if err := identity.ValidateToken(runtimeKey); err != nil {
		t.Errorf("handoff: runtime key: %v", err)
	}

	var phase string
	err = db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT object FROM facts WHERE subject=? AND predicate='phase'`, string(workID)).Scan(&phase)
	})
	if err != nil || phase != "build" {
		t.Errorf("handoff: phase fact = %q err %v, want build", phase, err)
	}
	var unverified int
	err = db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM facts WHERE predicate='source_fingerprint_state' AND object='unverified'`).Scan(&unverified)
	})
	if err != nil || unverified != 3 {
		t.Errorf("handoff: unverified fingerprint facts = %d err %v, want 3", unverified, err)
	}

	assertOpTerminal(t, db, "handoff.attach")

	// The consumed checkpoint was committed to the control repository.
	want := headSubject(t, runner, root)
	if want != fmt.Sprintf("homonto: attach %d", gen) {
		t.Errorf("handoff: HEAD subject = %q, want attach commit", want)
	}
	committed := gitShow(t, runner, root, "HEAD:.homonto/checkpoint.json")
	onDisk, err := os.ReadFile(CheckpointPath(root))
	if err != nil {
		t.Fatalf("handoff: read checkpoint: %v", err)
	}
	if string(committed) != string(onDisk) {
		t.Errorf("handoff: committed checkpoint differs from on-disk checkpoint")
	}
}

// assertOpTerminal verifies every operation of kind reached a terminal state.
func assertOpTerminal(t *testing.T, db *store.DB, kind string) {
	t.Helper()
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT state FROM operations WHERE kind=?`, kind)
		if err != nil {
			return err
		}
		defer rows.Close()
		states := map[string]int{}
		for rows.Next() {
			var state string
			if err := rows.Scan(&state); err != nil {
				return err
			}
			states[state]++
		}
		if len(states) == 0 {
			return fmt.Errorf("no %s operations journaled", kind)
		}
		for state := range states {
			if state != store.OpFinalized && state != store.OpRolledBack {
				return fmt.Errorf("%s operation in non-terminal state %s", kind, state)
			}
		}
		return nil
	})
	if err != nil {
		t.Errorf("handoff: %v", err)
	}
}

// metaValue reads one meta key from a database on root.
func metaValue(t *testing.T, root, key string) string {
	t.Helper()
	db, err := store.Open(context.Background(), RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime: %v", err)
	}
	defer func() { _ = db.Close() }()
	var value string
	err = db.View(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRowContext(context.Background(), `SELECT value FROM meta WHERE key=?`, key).Scan(&value)
	})
	if err != nil {
		return ""
	}
	return value
}

// decisionSummaries returns every decision summary recorded on root.
func decisionSummaries(t *testing.T, root string) []string {
	t.Helper()
	db, err := store.Open(context.Background(), RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime: %v", err)
	}
	defer func() { _ = db.Close() }()
	var out []string
	err = db.View(context.Background(), func(tx *store.Tx) error {
		rows, err := tx.QueryContext(context.Background(), `SELECT summary FROM decisions`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("handoff: decisions: %v", err)
	}
	return out
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

// setFailpointPrefix installs a hook that panics the nth time a point with
// the given prefix and suffix is reached. Points like
// "effect-applied-unrecorded:<op>:<seq>" embed the operation id, so exact
// matching is impossible before the run.
func setFailpointPrefix(t *testing.T, prefix, suffix string, nth int) (restore func()) {
	t.Helper()
	counts := map[string]int{}
	return operation.SetFailpointHook(func(p string) {
		if !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, suffix) {
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
		t.Fatal("handoff: expected simulated crash at failpoint")
	}
}

// recoverDB opens the runtime database of root and drives every pending
// operation to a terminal state.
func recoverDB(t *testing.T, root string) {
	t.Helper()
	db, err := store.Open(context.Background(), RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime for recovery: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := Recover(context.Background(), db); err != nil {
		t.Fatalf("handoff: recover: %v", err)
	}
}

// copyTree copies src to dst, skipping the runtime database, lease markers,
// git internals, and leftover securefs temporaries.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		skip := strings.HasPrefix(rel, ".git"+string(filepath.Separator)) || rel == ".git" ||
			strings.HasPrefix(rel, filepath.Join(".homonto", "runtime.db")) ||
			strings.HasPrefix(rel, filepath.Join(".homonto", "leases")) ||
			strings.HasPrefix(rel, ".securefs-tmp-")
		if skip {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("handoff: copy tree: %v", err)
	}
}
