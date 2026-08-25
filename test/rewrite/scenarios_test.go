package rewrite

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// The scenarios here are the ones that only happen when something has
// already gone differently than planned: work that fails its checks, a
// process that dies mid-workflow, a workspace picked up on another
// machine. Each is exercised elsewhere at the unit level; what these prove
// is that the pieces still meet when the happy path is not taken.

// breakingWorkspace answers implementer assignments with work that does
// not satisfy the member's check, up to a point.
//
// The point matters: an implementer that fails forever proves only that
// Homonto gives up. What has to be proven is that a repair round is issued
// AND that the repaired result is what reaches the archive.
type breakingWorkspace struct {
	*workspace
	failuresLeft int
}

// answer breaks the first N implementer results and does real work after.
func (b *breakingWorkspace) answer(t *testing.T, act protocol.Action) {
	t.Helper()
	if act.Kind != protocol.KindAssignment || act.Role != protocol.RoleImplementer ||
		strings.Contains(act.Reason, "integrate") || b.failuresLeft <= 0 {
		b.workspace.answer(t, act)
		return
	}
	if act.Repository.ID == b.assetsID {
		b.workspace.answer(t, act) // the assets member has no check to fail
		return
	}
	b.failuresLeft--
	// Committed, reported honestly, and simply wrong: the check is what
	// catches it, not the reporting.
	dir := filepath.Join(b.root, filepath.FromSlash(act.WorkingDirectory))
	writeFile(t, filepath.Join(dir, "src", "login.go"),
		"package src\n\nfunc Login() bool { return false /* still broken */ }\n")
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=T",
		"commit", "-m", "attempt")
	commit := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	b.report(t, act, protocol.ImplementerReport{
		Material: protocol.Material{
			Kind:    protocol.MaterialGitCommit,
			Commit:  commit,
			Content: fingerprint.Bytes("fixture-material", []byte(commit)),
		},
		ChangedPaths: []string{"src/login.go"},
	})
}

// TestFailingChecksProduceARepairRoundAndThenArchive is the scenario a
// verification step exists for.
//
// Its real subject is what happens AFTER the failure: a repair round that
// returned straight to the checks would archive material that was never
// integrated, and the record would describe work the integration branch
// does not contain. So the assertion is on the archived record and on the
// integration branch, not merely on "a repair happened".
func TestFailingChecksProduceARepairRoundAndThenArchive(t *testing.T) {
	b := &breakingWorkspace{workspace: newWorkspace(t), failuresLeft: 1}

	if out, err := b.run(t, "task", "start", "fix-login", "--goal", "Make login work."); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}

	repaired := false
	for i := 0; i < 60; i++ {
		resp := b.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if strings.Contains(strings.ToLower(act.Reason), "repair") {
				repaired = true
			}
			b.answer(t, act)
		}
	}
	if !repaired {
		t.Fatal("a failing check produced no repair round")
	}
	if b.failuresLeft != 0 {
		t.Fatalf("the fixture never got to fail: %d failures left", b.failuresLeft)
	}

	entries, err := os.ReadDir(filepath.Join(b.root, filepath.FromSlash(artifact.TasksArchiveDir)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the repaired task did not reach the archive (%d entries, %v)", len(entries), err)
	}

	// The repaired material must be what was integrated. A repair that
	// bypassed integration would leave the branch holding the broken
	// attempt while the record says the checks passed.
	api := filepath.Join(b.root, "services", "api")
	branch := integrationBranch(t, api)
	shown := git(t, api, "show", branch+":src/login.go")
	if !strings.Contains(shown, "return true") {
		t.Errorf("the integration branch holds the failed attempt, not the repair:\n%s", shown)
	}
	if strings.Contains(shown, "still broken") {
		t.Errorf("the integration branch still carries the broken attempt:\n%s", shown)
	}
}

// integrationBranch finds the single integration branch in a member.
func integrationBranch(t *testing.T, dir string) string {
	t.Helper()
	for _, line := range strings.Split(git(t, dir, "branch", "--list", "--format=%(refname:short)"), "\n") {
		if name := strings.TrimSpace(line); strings.HasPrefix(name, "homonto/integration/") {
			return name
		}
	}
	t.Fatal("no integration branch")
	return ""
}

// TestAnInterruptedWorkflowResumesWhereItStopped simulates the process
// dying mid-workflow.
//
// Every command here already opens and closes its own runtime — so the
// interruption is not simulated by tearing down a handle, it is real:
// actions are answered, nothing tells Homonto the session ended, and the
// next command is a fresh process against the same journal. What must not
// happen is the workflow starting over, skipping ahead, or re-issuing
// work that was already reported.
func TestAnInterruptedWorkflowResumesWhereItStopped(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login", "--goal", "Make login work."); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}

	// Answer two groups, then walk away mid-workflow.
	var answered []identityPair
	for i := 0; i < 2; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			t.Fatal("the workflow completed before it could be interrupted")
		}
		for _, act := range resp.Actions {
			answered = append(answered, identityPair{string(act.ID), string(act.Role)})
			w.answer(t, act)
		}
	}

	// A fresh process must not hand back work that was already reported.
	resumed := w.next(t)
	if resumed.State == protocol.NextComplete {
		t.Fatal("the interrupted workflow reported itself complete")
	}
	for _, act := range resumed.Actions {
		for _, prior := range answered {
			if string(act.ID) == prior.id {
				t.Errorf("action %s (%s) was re-issued after being reported", prior.id, prior.role)
			}
		}
	}

	// And the workflow still finishes.
	for i := 0; i < 60; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			w.answer(t, act)
		}
	}
	entries, err := os.ReadDir(filepath.Join(w.root, filepath.FromSlash(artifact.TasksArchiveDir)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the resumed task did not reach the archive (%d entries, %v)", len(entries), err)
	}
}

// identityPair is one answered action, kept for the re-issue check.
type identityPair struct{ id, role string }

// TestPortableHandoffResumesOnAnotherMachine drives the handoff the way it
// is meant to be used: work started here, picked up there.
//
// The interesting part is not that a file can be written and read. It is
// that the second machine's Homonto does not consider the work its own
// until it attaches, and that attaching mints a new runtime key — so the
// freshness tokens the first machine handed out stop working. A token that
// survived the move would let two machines answer the same action.
func TestPortableHandoffResumesOnAnotherMachine(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login", "--goal", "Make login work."); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	// Take an action but do not answer it, so a live token exists to test.
	outstanding := w.next(t)
	if len(outstanding.Actions) == 0 {
		t.Fatal("no outstanding action to hand off")
	}
	stale := outstanding.Actions[0]

	out, err := w.run(t, "handoff", "fix-login")
	if err != nil {
		t.Fatalf("handoff: %v\n%s", err, out)
	}

	// "Another machine": a copy of the tree, with the runtime state the
	// handoff is meant to make unnecessary removed.
	other := copyTree(t, w.root)
	elsewhere := &workspace{
		root: other, cfg: w.cfg,
		controlID: w.controlID, apiID: w.apiID, assetsID: w.assetsID,
	}
	if err := os.RemoveAll(filepath.Join(other, ".homonto", "runtime.db")); err != nil {
		t.Fatalf("remove runtime: %v", err)
	}

	// Member locations are CONFIRMED, never guessed: a checkpoint names
	// repository ids, and where those live on this machine is something
	// only a human can assert.
	if out, err := elsewhere.run(t, "attach",
		"--member", string(w.apiID)+"="+filepath.Join(other, "services", "api"),
		"--member", string(w.assetsID)+"="+filepath.Join(other, "assets")); err != nil {
		t.Fatalf("attach: %v\n%s", err, out)
	}

	// The first machine's token must no longer be honoured.
	body, execErr := elsewhere.runInput(t, protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        stale.ID,
		FreshnessToken:  stale.FreshnessToken,
		Role:            stale.Role,
		Session:         session(t),
		Report:          mustJSON(t, readOnlyReport(stale.Role)),
	}, "report")
	if execErr == nil {
		t.Errorf("a token minted before the handoff was still accepted:\n%s", body)
	}

	// And the work continues to completion on the second machine.
	for i := 0; i < 60; i++ {
		resp := elsewhere.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			elsewhere.answer(t, act)
		}
	}
	entries, err := os.ReadDir(filepath.Join(other, filepath.FromSlash(artifact.TasksArchiveDir)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the handed-off task did not reach the archive on the second machine (%d entries, %v)",
			len(entries), err)
	}
}

// copyTree duplicates a workspace, standing in for the same tree reaching
// a second machine.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	dst = filepath.Join(dst, "elsewhere")
	if out, err := exec.Command("cp", "-a", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("copy workspace: %v\n%s", err, out)
	}
	return dst
}

// mustJSON encodes a fixture payload.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// TestStartingAWorkAnchorsItToThisMachine.
//
// Exactly one top-level Task or Change is active in a workspace, so
// starting one is also when it becomes THIS machine's: the members are
// leased, the sentinel is written, and the checkpoint names the work. Skip
// any of that and `homonto handoff` has nothing to hand over — which is
// how the whole portable path came to be unreachable.
func TestStartingAWorkAnchorsItToThisMachine(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(filepath.Join(w.root, ".homonto", "leases"))
	if err != nil {
		t.Fatalf("read the lease directory: %v", err)
	}
	active := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".active") {
			active++
		}
	}
	if active != 1 {
		t.Errorf("%d lease sentinels, want exactly 1", active)
	}

	cp := readCheckpoint(t, w.root)
	if cp.Work == nil || cp.Work.Name != "fix-login" {
		t.Fatalf("the checkpoint names %v, want the started work", cp.Work)
	}
	// Every member is anchored, the control repository included: attach
	// has to know where each one was, and the control is the one that
	// carries the documents.
	if len(cp.Members) != len(w.cfg.Members)+1 {
		t.Errorf("the checkpoint anchors %d members, want %d plus the control",
			len(cp.Members), len(w.cfg.Members))
	}
}

// TestArchivingReleasesTheWorkspace: a finished work must not keep holding
// members. Otherwise the next work in the workspace can never be anchored,
// and the failure appears much later as a handoff that refuses for no
// visible reason.
func TestArchivingReleasesTheWorkspace(t *testing.T) {
	w := newWorkspace(t)
	if out, err := w.run(t, "task", "start", "fix-login", "--goal", "Make login work."); err != nil {
		t.Fatalf("task start: %v\n%s", err, out)
	}
	for i := 0; i < 60; i++ {
		resp := w.next(t)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			w.answer(t, act)
		}
	}

	entries, err := os.ReadDir(filepath.Join(w.root, ".homonto", "leases"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the lease directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".active") {
			t.Errorf("an archived task still holds the workspace: %s", entry.Name())
		}
	}
	if cp := readCheckpoint(t, w.root); cp.Work != nil {
		t.Errorf("the checkpoint still names %s after it archived", cp.Work.Name)
	}

	// The workspace is usable again: the next work anchors.
	if out, err := w.run(t, "task", "start", "next-thing"); err != nil {
		t.Fatalf("task start after archive: %v\n%s", err, out)
	}
	if cp := readCheckpoint(t, w.root); cp.Work == nil || cp.Work.Name != "next-thing" {
		t.Errorf("the next work was not anchored: %v", cp.Work)
	}
}

// readCheckpoint loads the committed portable record.
func readCheckpoint(t *testing.T, root string) checkpoint.Checkpoint {
	t.Helper()
	cp, _, err := checkpoint.Load(filepath.Join(root, ".homonto", "checkpoint.json"))
	if err != nil {
		t.Fatalf("read the checkpoint: %v", err)
	}
	return cp
}

// TestWorkRefusesToStartOverUncommittedChanges.
//
// An assignment cannot be cut from a dirty member — dirty trees are
// rejected, never tidied — and that refusal used to arrive several steps
// in: after the work existed, the document was written, and the explorer
// and skeptic had been answered. The tree was dirty the whole time.
//
// The refusal names the member and the files, because "something is
// dirty" sends someone hunting through every repository they own.
func TestWorkRefusesToStartOverUncommittedChanges(t *testing.T) {
	w := newWorkspace(t)
	writeFile(t, filepath.Join(w.root, "services", "api", "src", "scratch.go"),
		"package src\n\n// left behind\n")

	out, err := w.run(t, "task", "start", "fix-login")
	if err == nil {
		t.Fatalf("work started over uncommitted changes:\n%s", out)
	}
	for _, want := range []string{"services/api", "src/scratch.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// Nothing was created: a refused start must not leave a half-made work.
	entries, err := os.ReadDir(filepath.Join(w.root, filepath.FromSlash(artifact.TasksDir)))
	if err != nil {
		t.Fatalf("read the task directory: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Errorf("a refused start left %s behind", entry.Name())
		}
	}

	// Committing it makes the workspace startable.
	api := filepath.Join(w.root, "services", "api")
	git(t, api, "add", "-A")
	git(t, api, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "scratch")
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("task start after committing: %v\n%s", err, out)
	}
}

// TestTheControlRepositoryMayBeDirtyAtStart. The control holds the
// workflow documents Homonto itself writes, so it is dirty as a matter of
// course. Refusing on it would make the workspace unusable after the first
// task.
func TestTheControlRepositoryMayBeDirtyAtStart(t *testing.T) {
	w := newWorkspace(t)
	writeFile(t, filepath.Join(w.root, "notes.md"), "# scratch\n")
	if out, err := w.run(t, "task", "start", "fix-login"); err != nil {
		t.Fatalf("a dirty control repository blocked the start: %v\n%s", err, out)
	}
}
