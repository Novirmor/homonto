package ontocli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// runGit runs a git subcommand in dir, failing the test on error. It is used
// only to build and manipulate the temp git repos these tests exercise —
// never to touch the real repository.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// prepWorkspace builds a temp git repo that passes gate(): a homonto.toml
// declaring [frameworks.onto] plus an applied onto catalog directory. It is
// committed so the repo starts clean, and callers control dirtiness from
// there by writing further, uncommitted files.
func prepWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatalf("failed to create catalog dir: %v", err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

// seedChange writes onto-state.yaml for name at phase, plus every artifact
// RequiredArtifacts(phase) names (empty content), and any extraFiles
// (relative to the change dir, written with placeholder content). It does
// not commit; callers commit explicitly so "clean" vs "dirty" cases are
// under test control.
func seedChange(t *testing.T, root, name, phase string, extraFiles ...string) {
	t.Helper()
	changeDir := filepath.Join(root, "docs", "changes", name)

	st := ontostate.State{Change: name, Workflow: "full", Phase: phase, Created: "2026-07-10"}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("seedChange: saving state: %v", err)
	}

	for _, f := range ontostate.RequiredArtifacts(phase, st.Workflow) {
		if f == "onto-state.yaml" {
			continue
		}
		path := filepath.Join(changeDir, f)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		writeFile(t, path, "")
	}

	for _, f := range extraFiles {
		writeFile(t, filepath.Join(changeDir, f), "")
	}
}

// commitAll stages and commits everything currently in root, leaving the
// worktree clean.
func commitAll(t *testing.T, root, msg string) {
	t.Helper()
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", msg)
}

// dirtyWorktree writes an untracked file into root so the worktree is no
// longer clean, without committing it.
func dirtyWorktree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "uncommitted.txt"), "dirty\n")
}

// --- worktreeDirty ---

func TestWorktreeDirty_CleanRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")

	dirty, determinable := worktreeDirty(dir)
	if !determinable {
		t.Fatal("worktreeDirty() determinable = false, want true")
	}
	if dirty {
		t.Error("worktreeDirty() dirty = true, want false")
	}
}

func TestWorktreeDirty_DirtyRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")

	writeFile(t, filepath.Join(dir, "b.txt"), "b\n")

	dirty, determinable := worktreeDirty(dir)
	if !determinable {
		t.Fatal("worktreeDirty() determinable = false, want true")
	}
	if !dirty {
		t.Error("worktreeDirty() dirty = false, want true")
	}
}

func TestWorktreeDirty_NonRepo(t *testing.T) {
	dir := t.TempDir()

	_, determinable := worktreeDirty(dir)
	if determinable {
		t.Error("worktreeDirty() determinable = true, want false for a non-repo dir")
	}
}

// --- advance command ---

// TestAdvanceCommand_OpenToDesign verifies a normal, unblocked advance: an
// open change with only open's own required artifacts (no design.md, which
// is design's deliverable, not open's) moves to design and exits 0.
func TestAdvanceCommand_OpenToDesign(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "open")
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ProposalApproved = "2026-07-22 approved" })
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "design" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "design")
	}
}

// TestAdvanceCommand_MissingDesignDocRefused verifies that advancing a
// design-phase change without design.md — one of design's own required
// deliverables — is refused, names design.md in the error, and leaves the
// phase unchanged.
func TestAdvanceCommand_MissingDesignDocRefused(t *testing.T) {
	dir := prepWorkspace(t)
	changeDir := filepath.Join(dir, "docs", "changes", "feature-x")
	st := ontostate.State{Change: "feature-x", Workflow: "full", Phase: "design", Created: "2026-07-10"}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("saving onto-state.yaml: %v", err)
	}
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "")
	// design.md deliberately omitted: design's own required artifact.
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	if !strings.Contains(err.Error(), "design.md") {
		t.Errorf("execute() error = %q, want it to mention %q", err.Error(), "design.md")
	}

	loaded, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if loaded.Phase != "design" {
		t.Errorf("st.Phase = %q, want unchanged %q", loaded.Phase, "design")
	}
}

// TestAdvanceCommand_BuildToVerifyBlockedByUncheckedTask verifies that a
// build change whose tasks.md still has an unchecked item cannot advance to
// verify, even though every artifact RequiredArtifacts("build") names
// (build's own cumulative deliverables) is present.
func TestAdvanceCommand_BuildToVerifyBlockedByUncheckedTask(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "build")
	changeDir := filepath.Join(dir, "docs", "changes", "feature-x")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] todo\n")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	st, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "build" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "build")
	}
}

// TestAdvanceCommand_TerminalPhaseRefused verifies that a change already at
// "close" (terminal) cannot advance further and the state is untouched.
func TestAdvanceCommand_TerminalPhaseRefused(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "close")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "close" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "close")
	}
}

// setVerifyResult rewrites the change's onto-state.yaml with verify.result set
// to the given value, preserving the rest of the seeded state. It is used by
// advance tests that must satisfy (or deliberately miss) the leaving-verify
// evidence gate.
func setVerifyResult(t *testing.T, root, name, result string) {
	t.Helper()
	statePath := filepath.Join(root, "docs", "changes", name, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		t.Fatalf("setVerifyResult: load: %v", err)
	}
	st.Verify.Result = result
	if err := ontostate.Save(statePath, st); err != nil {
		t.Fatalf("setVerifyResult: save: %v", err)
	}
}

// TestAdvanceCommand_LeavingVerifyBlockedWithoutPass verifies that a verify-
// phase change whose verify.result is still pending cannot advance, the error
// names the missing verification, and the phase is left at verify.
func TestAdvanceCommand_LeavingVerifyBlockedWithoutPass(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "verify")
	setVerifyResult(t, dir, "feature-x", "pending")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	if !strings.Contains(err.Error(), "verify.result") {
		t.Errorf("execute() error = %q, want it to mention %q", err.Error(), "verify.result")
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "verify" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "verify")
	}
}

// TestAdvanceCommand_LeavingVerifyAllowedWithPass verifies that once
// verify.result=pass is recorded, a clean-worktree verify change advances into
// close.
func TestAdvanceCommand_LeavingVerifyAllowedWithPass(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "verify")
	writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), "Result: pass\n")
	setVerifyResult(t, dir, "feature-x", "pass")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "close" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "close")
	}
}

// TestAdvanceCommand_EnteringBuildBlockedWithoutIsolation verifies a design-
// phase change with no isolation set cannot advance into build, the error
// names the missing isolation, and the phase is left at design.
func TestAdvanceCommand_EnteringBuildBlockedWithoutIsolation(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "design")
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ApproachConfirmed = "2026-07-22 approach" })
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	if !strings.Contains(err.Error(), "isolation") {
		t.Errorf("execute() error = %q, want it to mention %q", err.Error(), "isolation")
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "design" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "design")
	}
}

// TestAdvanceCommand_EnteringBuildAllowedWithIsolation verifies that once
// isolation is chosen, a design change advances into build.
func TestAdvanceCommand_EnteringBuildAllowedWithIsolation(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "design")
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ApproachConfirmed = "2026-07-22 approach" })
	if _, err := runOnto(t, "set", "isolation", "feature-x", "worktree", "--dir", dir); err != nil {
		t.Fatalf("set isolation: %v", err)
	}
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "build" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "build")
	}
}

// TestAdvanceCommand_EnteringBuildRefusesInvalidIsolationValue verifies that a
// hand-edited state carrying an unknown isolation value (e.g. "vm") cannot
// satisfy the build-entry gate. The current check is only `isolation == ""`, so
// any non-empty value passes; validation must reject the malformed value. F9.
func TestAdvanceCommand_EnteringBuildRefusesInvalidIsolationValue(t *testing.T) {
	dir := prepWorkspace(t)
	changeDir := filepath.Join(dir, "docs", "changes", "feature-x")
	seedChange(t, dir, "feature-x", "design")
	// Hand-edit the state with an invalid isolation value (not branch/worktree).
	st, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	st.Isolation = "vm"
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("save: %v", err)
	}
	commitAll(t, dir, "seed change")

	if _, err := runOnto(t, "advance", "feature-x", "--dir", dir); err == nil {
		t.Fatalf("advance must reject an unknown isolation value")
	}
	got, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Phase != "design" {
		t.Errorf("phase advanced to %q on a malformed state; want design", got.Phase)
	}
}

// TestAdvanceCommand_VerifyToCloseBlockedByDirtyWorktree verifies that
// entering "close" is refused when the worktree is dirty, even though every
// required artifact is present and verify.result=pass, and leaves the phase
// unchanged. verify.result is set to pass so the dirty-worktree gate — not the
// leaving-verify evidence gate — is what blocks.
func TestAdvanceCommand_VerifyToCloseBlockedByDirtyWorktree(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "verify")
	writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), "Result: pass\n")
	setVerifyResult(t, dir, "feature-x", "pass")
	commitAll(t, dir, "seed change")
	dirtyWorktree(t, dir)

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "verify" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "verify")
	}
}

// TestAdvanceCommand_DirtyWorktreeWarnsAndProceedsForNonCloseTransition
// verifies that a dirty worktree only blocks entering "close"; any other
// transition proceeds, printing a warning to stderr.
func TestAdvanceCommand_DirtyWorktreeWarnsAndProceedsForNonCloseTransition(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "open")
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ProposalApproved = "2026-07-22 approved" })
	commitAll(t, dir, "seed change")
	dirtyWorktree(t, dir)

	cmd := NewRootCmd()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "design" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "design")
	}
	if !bytes.Contains(errOut.Bytes(), []byte("warning")) {
		t.Errorf("stderr = %q, want it to contain %q", errOut.String(), "warning")
	}
}

// mutateState loads, mutates, and saves a change's onto-state.yaml.
func mutateState(t *testing.T, root, name string, mutate func(*ontostate.State)) {
	t.Helper()
	statePath := filepath.Join(root, "docs", "changes", name, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		t.Fatalf("mutateState: load: %v", err)
	}
	mutate(&st)
	if err := ontostate.Save(statePath, st); err != nil {
		t.Fatalf("mutateState: save: %v", err)
	}
}

// TestAdvanceCommand_LeavingOpenRequiresProposalApproved: a full change at
// open with every artifact present still refuses to advance until the
// artifact-review gate's evidence token is recorded; the refusal names the
// setter. Presets are exempt (their scope gate lives in the preset skill).
func TestAdvanceCommand_LeavingOpenRequiresProposalApproved(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "open")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want proposal-approved refusal")
	}
	if !strings.Contains(err.Error(), "proposal-approved") {
		t.Errorf("error %q must name the proposal-approved setter", err.Error())
	}

	// Token recorded → advances.
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ProposalApproved = "2026-07-22 approved" })
	commitAll(t, dir, "record token")
	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("advance with token: %v", err)
	}
}

// TestAdvanceCommand_PresetLeavesOpenWithoutProposalApproved: fix presets are
// exempt from the full-only proposal_approved token.
func TestAdvanceCommand_PresetLeavesOpenWithoutProposalApproved(t *testing.T) {
	dir := prepWorkspace(t)
	changeDir := filepath.Join(dir, "docs", "changes", "fix-y")
	st := ontostate.State{Change: "fix-y", Workflow: "fix", Phase: "open", Created: "2026-07-10"}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] fix\n")
	commitAll(t, dir, "seed fix")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "fix-y", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("preset advance must not need proposal_approved: %v", err)
	}
}

// TestAdvanceCommand_EnteringBuildRequiresApproachConfirmed: isolation alone
// no longer suffices — the approach gate's token is required too (full only).
func TestAdvanceCommand_EnteringBuildRequiresApproachConfirmed(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "design")
	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.Isolation = "branch" })
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want approach-confirmed refusal")
	}
	if !strings.Contains(err.Error(), "approach-confirmed") {
		t.Errorf("error %q must name the approach-confirmed setter", err.Error())
	}

	mutateState(t, dir, "feature-x", func(s *ontostate.State) { s.ApproachConfirmed = "2026-07-22 approach B" })
	commitAll(t, dir, "record token")
	cmd = NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("advance with token: %v", err)
	}
}

// TestAdvanceCommand_LeavingVerifyCrossChecksReport: verify.result=pass in
// state is not enough — verification.md's own Result: line must agree.
func TestAdvanceCommand_LeavingVerifyCrossChecksReport(t *testing.T) {
	cases := []struct {
		name    string
		report  string
		wantErr string // empty = advance succeeds
	}{
		{"report says fail", "# V\nResult: fail\n", "Result: fail"},
		{"accepted deviations still pass", "# V\nResult: pass (2 accepted deviations)\n", ""},
		{"no Result line", "# V\nall good probably\n", "Result:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedChange(t, dir, "feature-x", "verify")
			writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), tc.report)
			setVerifyResult(t, dir, "feature-x", "pass")
			commitAll(t, dir, "seed change")

			cmd := NewRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})
			err := cmd.Execute()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("advance should pass: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("advance should refuse (%s)", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q must mention %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestAdvanceCommand_ToBuildPresetOneCall: a preset with isolation set walks
// open→design→build through one gated call; each hop's gates still run.
func TestAdvanceCommand_ToBuildPresetOneCall(t *testing.T) {
	dir := prepWorkspace(t)
	changeDir := filepath.Join(dir, "docs", "changes", "fix-y")
	st := ontostate.State{Change: "fix-y", Workflow: "fix", Phase: "open", Created: "2026-07-10", Isolation: "branch"}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] fix\n")
	commitAll(t, dir, "seed fix")

	if _, err := runOnto(t, "advance", "fix-y", "--to", "build", "--dir", dir); err != nil {
		t.Fatalf("advance --to build: %v", err)
	}
	loaded, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != "build" {
		t.Errorf("phase = %q, want build", loaded.Phase)
	}
}

// TestAdvanceCommand_ToBuildRefusalCases: full workflows advance one gate at
// a time; only build is a valid --to target; a failing hop surfaces its own
// gate error.
func TestAdvanceCommand_ToBuildRefusalCases(t *testing.T) {
	t.Run("full refused", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedChange(t, dir, "feature-x", "open")
		commitAll(t, dir, "seed")
		_, err := runOnto(t, "advance", "feature-x", "--to", "build", "--dir", dir)
		if err == nil || !strings.Contains(err.Error(), "one gate at a time") {
			t.Fatalf("full --to build must refuse naming the rule, got: %v", err)
		}
	})
	t.Run("non-build target refused", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedChange(t, dir, "feature-x", "open")
		commitAll(t, dir, "seed")
		_, err := runOnto(t, "advance", "feature-x", "--to", "verify", "--dir", dir)
		if err == nil || !strings.Contains(err.Error(), "build") {
			t.Fatalf("--to verify must refuse, got: %v", err)
		}
	})
	t.Run("failing hop surfaces its gate", func(t *testing.T) {
		dir := prepWorkspace(t)
		changeDir := filepath.Join(dir, "docs", "changes", "fix-y")
		st := ontostate.State{Change: "fix-y", Workflow: "fix", Phase: "open", Created: "2026-07-10"} // no isolation
		if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
		writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] fix\n")
		commitAll(t, dir, "seed fix")
		_, err := runOnto(t, "advance", "fix-y", "--to", "build", "--dir", dir)
		if err == nil || !strings.Contains(err.Error(), "isolation") {
			t.Fatalf("the design→build hop's isolation gate must surface, got: %v", err)
		}
	})
}

// TestAdvanceCommand_ToBuildValidatesBeforeRead: --to build must validate the
// change name (and the framework gate) before touching any path — a traversal
// name must never cause an out-of-workspace read.
func TestAdvanceCommand_ToBuildValidatesBeforeRead(t *testing.T) {
	dir := prepWorkspace(t)
	_, err := runOnto(t, "advance", "../../../etc/passwd", "--to", "build", "--dir", dir)
	if err == nil {
		t.Fatal("traversal name must refuse")
	}
	if strings.Contains(err.Error(), "loading state") || strings.Contains(err.Error(), "yaml") {
		t.Errorf("refusal must come from name validation, not a file read: %v", err)
	}
}

// TestAdvanceCommand_ToBuildRefusesAtOrPastBuild: --to build never advances a
// change that is already at (or past) build — it must not walk past its target.
func TestAdvanceCommand_ToBuildRefusesAtOrPastBuild(t *testing.T) {
	for _, phase := range []string{"build", "verify"} {
		dir := prepWorkspace(t)
		changeDir := filepath.Join(dir, "docs", "changes", "fix-y")
		st := ontostate.State{Change: "fix-y", Workflow: "fix", Phase: phase, Created: "2026-07-10", Isolation: "branch"}
		if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
		writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] fix\n")
		commitAll(t, dir, "seed "+phase)
		_, err := runOnto(t, "advance", "fix-y", "--to", "build", "--dir", dir)
		if err == nil {
			t.Fatalf("--to build at %s must refuse", phase)
		}
		loaded, _ := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
		if loaded.Phase != phase {
			t.Errorf("phase mutated %s → %s; --to build must not move a change at/past build", phase, loaded.Phase)
		}
	}
}

// TestVerificationResultLine_SharedScannerSemantics: the verify exit and the
// phase derivation read the SAME Result line — leading whitespace tolerated,
// first Result: line wins, and "pass" must be the exact word (a suffix like
// "(2 accepted deviations)" is allowed; "passing" is not).
func TestVerificationResultLine_SharedScannerSemantics(t *testing.T) {
	cases := []struct {
		name    string
		report  string
		advance bool // advance out of verify should succeed
		derived string
	}{
		{"indented pass", "# V\n  Result: pass\n", true, "close"},
		{"first line wins over later pass", "Result: fail\nResult: pass\n", false, "verify"},
		{"passing is not pass", "Result: passing\n", false, "verify"},
		{"deviations suffix", "Result: pass (2 accepted deviations)\n", true, "close"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedChange(t, dir, "feature-x", "verify")
			// All-checked tasks so a non-pass report derives verify (row 4),
			// not design — the fixture must make the intended row reachable.
			writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "tasks.md"), "- [x] done\n")
			writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), tc.report)
			setVerifyResult(t, dir, "feature-x", "pass")
			commitAll(t, dir, "seed")

			derived := ontostate.DeriveWorkingPhase(filepath.Join(dir, "docs", "changes", "feature-x"),
				ontostate.State{Change: "feature-x", Workflow: "full", Phase: "verify"})
			if derived != tc.derived {
				t.Errorf("derived = %q, want %q", derived, tc.derived)
			}

			_, err := runOnto(t, "advance", "feature-x", "--dir", dir)
			if tc.advance && err != nil {
				t.Errorf("advance should pass: %v", err)
			}
			if !tc.advance && err == nil {
				t.Error("advance should refuse")
			}
		})
	}
}
