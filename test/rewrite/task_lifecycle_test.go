// Package rewrite holds the rewritten workflow's end-to-end fixtures. They
// drive the real binary's command tree over a real workspace on disk —
// real Git repositories, real subprocess checks, real isolation areas —
// because everything below them is already unit-tested and what is left to
// prove is that the pieces meet.
package rewrite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/cli"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// requireTools skips when the fixture's external dependencies are absent.
func requireTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the lifecycle fixture needs a POSIX shell and git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

// workspace is a hybrid fixture: a Git control repository that also holds
// the workflow record, a second Git member, and a non-Git member.
type workspace struct {
	root      string
	cfg       workspacecfg.Config
	controlID identity.RepositoryID
	apiID     identity.RepositoryID
	assetsID  identity.RepositoryID
}

func mustID(t *testing.T) identity.RepositoryID {
	t.Helper()
	id, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return id
}

// git runs a git command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.ExecRunner{}.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return out
}

// writeFile writes a file, creating parents.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newWorkspace builds the hybrid fixture on disk.
func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	requireTools(t)
	// t.TempDir on macOS is a symlinked /var path; git reports the
	// physical one, and every path comparison here must agree with git.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}

	// Non-Git members keep their registration and lease slots in the
	// machine's state directory. Without this the suite writes into the
	// developer's home and leaves it there.
	t.Setenv(registration.StateRootEnv, t.TempDir())

	ws := &workspace{
		root:      root,
		controlID: mustID(t),
		apiID:     mustID(t),
		assetsID:  mustID(t),
	}

	// The control repository is the workspace root itself.
	if err := gitx.Init(context.Background(), gitx.ExecRunner{}, root); err != nil {
		t.Fatalf("git init control: %v", err)
	}
	writeFile(t, filepath.Join(root, "README.md"), "# workspace\n")
	writeFile(t, filepath.Join(root, ".gitignore"), ".homonto/\nservices/\nassets/\n")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "seed")

	// A second Git member.
	api := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatalf("mkdir api: %v", err)
	}
	if err := gitx.Init(context.Background(), gitx.ExecRunner{}, api); err != nil {
		t.Fatalf("git init api: %v", err)
	}
	writeFile(t, filepath.Join(api, "src", "login.go"), "package src\n\nfunc Login() bool { return false }\n")
	writeFile(t, filepath.Join(api, "check.sh"), "#!/bin/sh\ngrep -q 'return true' src/login.go\n")
	if err := os.Chmod(filepath.Join(api, "check.sh"), 0o755); err != nil {
		t.Fatalf("chmod check.sh: %v", err)
	}
	git(t, api, "add", "-A")
	git(t, api, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "seed")

	// A non-Git member: a plain directory of assets.
	assets := filepath.Join(root, "assets")
	writeFile(t, filepath.Join(assets, "logo.txt"), "old logo\n")

	ws.cfg = workspacecfg.Config{
		SchemaVersion: workspacecfg.CurrentSchemaVersion,
		Workspace: workspacecfg.Workspace{
			ID:       mustWorkspaceID(t),
			Workflow: workspacecfg.WorkflowTask,
		},
		Control: workspacecfg.Control{ID: ws.controlID, Path: "."},
		Members: []workspacecfg.Member{
			{
				ID: ws.apiID, Path: "services/api", Kind: workspacecfg.KindGit,
				Verification: []workspacecfg.Check{{
					Name: "login-works", Command: []string{"/bin/sh", "check.sh"},
					// PATH is allowlisted explicitly: the runner forwards
					// nothing ambient, so a script that calls grep must say
					// where it may look for it.
					Environment: []string{"PATH"}, Timeout: "30s",
				}},
			},
			{ID: ws.assetsID, Path: "assets", Kind: workspacecfg.KindNonGit},
		},
	}
	manifest, err := workspacecfg.Marshal(ws.cfg)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeFile(t, filepath.Join(root, app.ControlDir, app.ManifestName), string(manifest))
	return ws
}

func mustWorkspaceID(t *testing.T) identity.WorkspaceID {
	t.Helper()
	id, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("NewWorkspaceID: %v", err)
	}
	return id
}

// args prepends the workspace flag so every fixture invocation goes
// through the real command path — the default opener, the real read-only
// handling, the real root resolution. An injected opener would let the
// harness quietly bypass exactly the behaviour these tests exist to
// check.
func (w *workspace) args(args []string) []string {
	return append([]string{"--workspace", w.root}, args...)
}

// run executes one workflow command against the fixture and returns its
// stdout. It goes through the real cobra tree, not the App, so the fixture
// proves the commands are wired.
func (w *workspace) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := cli.NewWorkflowRootCmd(nil)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(w.args(args))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := root.ExecuteContext(ctx)
	return out.String(), err
}

// runInput executes a command with a JSON payload on stdin.
func (w *workspace) runInput(t *testing.T, payload any, args ...string) (string, error) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	root := cli.NewWorkflowRootCmd(func(ctx context.Context, _ string, readOnly bool) (*app.App, error) {
		return app.Open(ctx, app.Options{Root: w.root, ReadOnly: readOnly})
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(bytes.NewReader(body))
	root.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	execErr := root.ExecuteContext(ctx)
	return out.String(), execErr
}

// next asks the CLI what to do and decodes the protocol payload.
func (w *workspace) next(t *testing.T) protocol.NextResponse {
	t.Helper()
	out, err := w.run(t, "next", "--json")
	if err != nil {
		t.Fatalf("next: %v\n%s", err, out)
	}
	resp, err := protocol.DecodeNextResponse(strings.NewReader(out))
	if err != nil {
		t.Fatalf("decode next response: %v\n%s", err, out)
	}
	if err := resp.Validate(); err != nil {
		t.Fatalf("next returned an invalid response: %v", err)
	}
	return resp
}

func session(t *testing.T) protocol.Session {
	t.Helper()
	id, err := identity.NewSessionID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return protocol.Session{
		HostID: id, Hostname: "fixture", PID: os.Getpid(),
		Executable: "/usr/bin/claude", StartedAt: time.Now().UTC(),
	}
}

// report submits a canned role report through the CLI.
func (w *workspace) report(t *testing.T, act protocol.Action, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	out, err := w.runInput(t, protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID,
		FreshnessToken:  act.FreshnessToken,
		Role:            act.Role,
		Session:         session(t),
		Report:          raw,
	}, "report")
	if err != nil {
		t.Fatalf("report(%s %s): %v\n%s", act.Role, act.ID, err, out)
	}
}

// readOnlyReport is the canned answer for a role that only observes.
func readOnlyReport(role protocol.Role) any {
	switch role {
	case protocol.RoleExplorer:
		return protocol.ExplorerReport{
			Facts:    []string{"login returns false"},
			Surfaces: []string{"services/api/src/login.go"},
			Tests:    []string{"check.sh"},
		}
	case protocol.RoleReviewer:
		return protocol.ReviewerReport{Acceptance: []string{"the checklist item is covered"}}
	default:
		return protocol.SkepticReport{Assumptions: []string{"only login.go decides the outcome"}}
	}
}

// implement performs the actual work in an isolation area and returns the
// implementer report describing it. This is what a real agent would do.
func (w *workspace) implement(t *testing.T, act protocol.Action) any {
	t.Helper()
	dir := filepath.Join(w.root, filepath.FromSlash(act.WorkingDirectory))
	// Which kind of area this is comes from the assignment's MEMBER, not
	// from probing the directory: a non-Git isolation area lives under the
	// control repository's tree, and git would happily claim it.
	if act.Repository.ID != w.assetsID {
		target := filepath.Join(dir, "src", "login.go")
		writeFile(t, target, "package src\n\nfunc Login() bool { return true }\n")
		git(t, dir, "add", "-A")
		git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=T",
			"commit", "-m", "make login work")
		commit := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
		return protocol.ImplementerReport{
			Material: protocol.Material{
				Kind:    protocol.MaterialGitCommit,
				Commit:  commit,
				Content: fingerprint.Bytes("fixture-material", []byte(commit)),
			},
			ChangedPaths: []string{"src/login.go"},
		}
	}
	// A non-Git isolation area: edit in place and return a patch manifest.
	target := filepath.Join(dir, "logo.txt")
	writeFile(t, target, "new logo\n")
	return protocol.ImplementerReport{
		Material: protocol.Material{
			Kind:          protocol.MaterialSnapshotPatch,
			PatchManifest: []string{"logo.txt"},
			Content:       fingerprint.Bytes("fixture-material", []byte(act.ID)),
		},
		ChangedPaths: []string{"logo.txt"},
	}
}

// integrate combines the parallel results in the integration area.
func (w *workspace) integrate(t *testing.T, act protocol.Action) any {
	t.Helper()
	dir := filepath.Join(w.root, filepath.FromSlash(act.WorkingDirectory))
	if act.Repository.ID != w.assetsID {
		commit := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
		return protocol.ImplementerReport{
			Material: protocol.Material{
				Kind:    protocol.MaterialGitCommit,
				Commit:  commit,
				Content: fingerprint.Bytes("fixture-integration", []byte(commit)),
			},
			ChangedPaths: []string{"src/login.go"},
		}
	}
	return protocol.ImplementerReport{
		Material: protocol.Material{
			Kind:          protocol.MaterialSnapshotPatch,
			PatchManifest: []string{"logo.txt"},
			Content:       fingerprint.Bytes("fixture-integration", []byte(act.ID)),
		},
		ChangedPaths: []string{"logo.txt"},
	}
}

// draft performs the host's document edit and finishes it through the CLI.
func (w *workspace) draft(t *testing.T, act protocol.Action, goal, checklist string) {
	t.Helper()
	abs := filepath.Join(w.root, filepath.FromSlash(act.Edit.Document))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", act.Edit.Document, err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", act.Edit.Document, err)
	}
	for i := range doc.Regions {
		switch doc.Regions[i].Region {
		case artifact.RegionTaskGoal:
			doc.Regions[i].Content = []byte(goal)
		case artifact.RegionTaskChecklist:
			doc.Regions[i].Content = []byte(checklist)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write %s: %v", act.Edit.Document, err)
	}
	out, err := w.run(t, "accept-edit",
		"--action", string(act.ID), "--token", string(act.Edit.GrantToken))
	if err != nil {
		t.Fatalf("accept-edit: %v\n%s", err, out)
	}
}

// answer dispatches one action to the right canned response.
func (w *workspace) answer(t *testing.T, act protocol.Action) {
	t.Helper()
	switch act.Kind {
	case protocol.KindEdit:
		w.draft(t, act,
			"Make login return true.\n",
			"- [ ] make login return true\n")
	case protocol.KindDecision:
		out, err := w.run(t, "decide",
			"--action", string(act.ID), "--token", string(act.FreshnessToken),
			"--choice", act.Decision.Choices[0].Value,
			"--rationale", "fixture decision",
			"--answer", answerFor(act))
		if err != nil {
			t.Fatalf("decide: %v\n%s", err, out)
		}
	default:
		if act.Role != protocol.RoleImplementer {
			w.report(t, act, readOnlyReport(act.Role))
			return
		}
		if strings.Contains(act.Reason, "integrate") {
			w.report(t, act, w.integrate(t, act))
			return
		}
		w.report(t, act, w.implement(t, act))
	}
}

// answerFor supplies the free answer question gates require and nothing
// else accepts.
func answerFor(act protocol.Action) string {
	if act.Decision != nil && act.Decision.QuestionID != "" {
		return "yes"
	}
	return ""
}

// TestTaskLifecycleReachesTheArchive drives a whole Task through the CLI
// over a hybrid workspace: two Git members and one non-Git member, all
// four roles, parallel implementation in real isolation areas, real
// integration, real subprocess checks, and one archived record.
func TestTaskLifecycleReachesTheArchive(t *testing.T) {
	w := newWorkspace(t)

	out, err := w.run(t, "task", "start", "fix-login", "--goal", "Make login work.")
	if err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	if !strings.Contains(out, "started task fix-login") {
		t.Fatalf("task start printed %q", out)
	}

	roles := map[protocol.Role]bool{}
	var groups []int
	for i := 0; i < 40; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		groups = append(groups, len(resp.Actions))
		for _, act := range resp.Actions {
			if act.Role != "" {
				roles[act.Role] = true
			}
			w.answer(t, act)
		}
	}

	// All four roles were used.
	for _, role := range []protocol.Role{
		protocol.RoleExplorer, protocol.RoleImplementer,
		protocol.RoleReviewer, protocol.RoleSkeptic,
	} {
		if !roles[role] {
			t.Errorf("the %s role was never assigned", role)
		}
	}

	// At least one group carried more than one action: read-only
	// assignments run in parallel whenever their dependencies are met.
	maximal := false
	for _, n := range groups {
		if n > 1 {
			maximal = true
		}
	}
	if !maximal {
		t.Errorf("no action group was maximal; groups were %v", groups)
	}

	// The record left the active tree for the archive, checked off, with
	// its evidence.
	status, err := w.run(t, "task", "status")
	if err != nil {
		t.Fatalf("task status: %v\n%s", err, status)
	}
	if !strings.Contains(status, "archived") {
		t.Fatalf("task status = %q, want an archived task", status)
	}
	entries, err := os.ReadDir(filepath.Join(w.root, filepath.FromSlash(artifact.TasksArchiveDir)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive holds %d entries, want 1", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(artifact.TasksArchiveDir), entries[0].Name()))
	if err != nil {
		t.Fatalf("read archived record: %v", err)
	}
	record := string(body)
	for _, want := range []string{"- [x] make login return true", "## Verification", "check.sh"} {
		if !strings.Contains(record, want) {
			t.Errorf("the archived record does not carry %q:\n%s", want, record)
		}
	}

	// Ready but never merged: the integration branch exists in the member
	// repository, the member's own branch is untouched, and nothing was
	// copied back.
	api := filepath.Join(w.root, "services", "api")
	branches := git(t, api, "branch", "--list", "--format=%(refname:short)")
	if !strings.Contains(branches, "homonto/integration/") {
		t.Fatalf("no integration branch was left ready: %q", branches)
	}
	head := strings.TrimSpace(git(t, api, "show", "main:src/login.go"))
	if strings.Contains(head, "return true") {
		t.Fatal("the integration was merged into the member's own branch; it must be left ready, never merged")
	}
	if dirty := strings.TrimSpace(git(t, api, "status", "--porcelain")); dirty != "" {
		t.Fatalf("the member's working tree was modified: %q", dirty)
	}
}

// TestNextIsIdempotentWhileAGroupIsOutstanding proves a host may re-ask
// without being handed new work.
func TestNextIsIdempotentWhileAGroupIsOutstanding(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	first := w.next(t)
	second := w.next(t)
	if len(first.Actions) != len(second.Actions) {
		t.Fatalf("re-asking changed the group: %d then %d actions",
			len(first.Actions), len(second.Actions))
	}
	for i := range first.Actions {
		if first.Actions[i].ID != second.Actions[i].ID {
			t.Fatalf("action %d changed id between identical next calls", i)
		}
		if first.Actions[i].FreshnessToken != second.Actions[i].FreshnessToken {
			t.Fatalf("action %d changed token between identical next calls", i)
		}
	}
}

// TestGuardRefusesAnOutOfScopeWrite proves the process gate is wired end
// to end through the CLI.
func TestGuardRefusesAnOutOfScopeWrite(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	// Walk to the first implementer assignment.
	var impl protocol.Action
	for i := 0; i < 20 && impl.ID == ""; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if act.Role == protocol.RoleImplementer {
				impl = act
				break
			}
		}
		if impl.ID != "" {
			break
		}
		for _, act := range resp.Actions {
			w.answer(t, act)
		}
	}
	if impl.ID == "" {
		t.Fatal("the fixture never reached an implementer assignment")
	}

	allowed := protocol.GuardRequest{
		Host: protocol.HostClaude, SessionID: session(t).HostID, Tool: "Write",
		WorkingDirectory: impl.WorkingDirectory,
		WritePaths:       []string{impl.WriteScope.Paths[0] + "/anything.go"},
	}
	out, err := w.runInput(t, allowed, "guard",
		"--action", string(impl.ID), "--token", string(impl.FreshnessToken))
	if err != nil {
		t.Fatalf("guard(in scope): %v\n%s", err, out)
	}
	if !strings.Contains(out, `"allow":true`) {
		t.Fatalf("an in-scope write was refused: %s", out)
	}

	refused := allowed
	refused.WritePaths = []string{".homonto/checkpoint.json"}
	out, err = w.runInput(t, refused, "guard",
		"--action", string(impl.ID), "--token", string(impl.FreshnessToken))
	if err == nil {
		t.Fatalf("guard(control state) succeeded: %s", out)
	}
	if !strings.Contains(out, `"allow":false`) || !strings.Contains(out, "protected_path") {
		t.Fatalf("guard decision = %s, want a protected_path refusal", out)
	}

	// A session with no permission at all writes nothing.
	out, err = w.runInput(t, allowed, "guard")
	if err == nil {
		t.Fatalf("guard(no permission) succeeded: %s", out)
	}
	if !strings.Contains(out, "no_permission") {
		t.Fatalf("guard decision = %s, want a no_permission refusal", out)
	}
}

// TestAbandonLeavesTheWorkInPlace proves abandoning stops the workflow
// without destroying anything.
func TestAbandonLeavesTheWorkInPlace(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	resp := w.next(t)
	for _, act := range resp.Actions {
		w.answer(t, act)
	}
	out, err := w.run(t, "task", "abandon")
	if err != nil {
		t.Fatalf("task abandon: %v\n%s", err, out)
	}
	if !strings.Contains(out, "left in place") {
		t.Fatalf("abandon printed %q", out)
	}
	if _, err := os.Stat(filepath.Join(w.root, filepath.FromSlash(artifact.TasksDir), "fix-login.md")); err != nil {
		t.Fatalf("the abandoned task's document was removed: %v", err)
	}
	status, err := w.run(t, "task", "status")
	if err != nil {
		t.Fatalf("task status: %v\n%s", err, status)
	}
	if !strings.Contains(status, "abandoned") {
		t.Fatalf("task status = %q, want an abandoned task", status)
	}
}

// TestNoActiveTaskIsRefusedNotGuessed proves the resume probe never picks
// a task for the user.
func TestNoActiveTaskIsRefusedNotGuessed(t *testing.T) {
	w := newWorkspace(t)
	out, err := w.run(t, "next", "--json")
	if err == nil {
		t.Fatalf("next with no task succeeded: %s", out)
	}
	if !strings.Contains(err.Error(), "no active work") {
		t.Fatalf("error = %v, want a refusal naming the absence of active work", err)
	}
}

// TestASecondWorkIsRefused: exactly one top-level Task or Change may be
// active in a workspace. Parallelism happens INSIDE that work, through
// subagents and isolated worktrees.
//
// Two top-level works would share every member, so their isolation areas,
// their integration branches, and their checks would each be measuring a
// tree the other is also changing — and only one of them could hold the
// leases that make the work portable. The refusal names the work in the
// way, because "already active" without saying which is useless.
func TestASecondWorkIsRefused(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	out, err := w.run(t, "task", "start", "fix-cache")
	if err == nil {
		t.Fatalf("a second task was started:\n%s", out)
	}
	if !strings.Contains(err.Error(), "fix-login") {
		t.Errorf("the refusal does not name the work in the way: %v", err)
	}

	// And the workspace is usable again once the first one is out of the
	// way — the refusal must be about activity, not about the name having
	// ever been used.
	if out, err := w.run(t, "task", "abandon", "fix-login"); err != nil {
		t.Fatalf("task abandon: %v\n%s", err, out)
	}
	if out, err := w.run(t, "task", "start", "fix-cache"); err != nil {
		t.Fatalf("task start after abandoning: %v\n%s", err, out)
	}
}
