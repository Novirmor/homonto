package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// seedCloseState writes st (a change at the close phase) plus every artifact
// ontostate.RequiredArtifacts("close") names (proposal.md, tasks.md,
// design.md, plan.md, verification.md), each with placeholder content. It
// does not commit; callers commit explicitly so "clean" vs "dirty" cases are
// under test control. Callers set st's evidence fields (Verify.Result,
// Close.Merged, Guides, Workflow) to exercise the close-phase evidence gates.
func seedCloseState(t *testing.T, root string, st ontostate.State) {
	t.Helper()
	changeDir := filepath.Join(root, "docs", "changes", st.Change)
	if err := os.MkdirAll(changeDir, 0o755); err != nil {
		t.Fatalf("seedCloseState: creating %s: %v", changeDir, err)
	}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("seedCloseState: saving state: %v", err)
	}
	for _, f := range ontostate.RequiredArtifacts("close", st.Workflow) {
		if f == "onto-state.yaml" {
			continue
		}
		writeFile(t, filepath.Join(changeDir, f), "")
	}
}

// seedClose writes a full-workflow change at "close" with the given deps and
// all close-phase evidence resolved (verify.result=pass, close.merged=true,
// guides=updated), so the close-phase evidence gates are satisfied and the
// existing structural gates (deps/dirty/no-clobber) are what remain under
// test. Callers exercising the evidence gates themselves use seedCloseState.
func seedClose(t *testing.T, root, name string, deps []string) {
	t.Helper()
	seedCloseState(t, root, ontostate.State{
		Change:         name,
		Workflow:       "full",
		Phase:          "close",
		Created:        "2026-07-10",
		Deps:           deps,
		Verify:         ontostate.Verify{Result: "pass"},
		Close:          ontostate.Close{Merged: true},
		Guides:         "updated",
		CloseConfirmed: "2026-07-22 close plan confirmed",
	})
}

// TestCloseCommand_Success verifies the happy path: a "close"-phase change
// with no deps, in a clean worktree, is archived into
// docs/changes/archive/<date>-<name>/ with Archived==true and Phase
// unchanged, and the original change directory is gone.
func TestCloseCommand_Success(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading archived onto-state.yaml: %v", err)
	}
	if !st.Archived {
		t.Errorf("st.Archived = false, want true")
	}
	if st.Phase != "close" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "close")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); !os.IsNotExist(err) {
		t.Errorf("original change dir stat err = %v, want IsNotExist", err)
	}
}

// TestCloseCommand_NonClosePhaseRefused verifies that a change not yet at
// "close" is refused and left in place.
func TestCloseCommand_NonClosePhaseRefused(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "demo", "build")
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
		t.Errorf("change dir should still exist, stat err = %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading in-place onto-state.yaml: %v", err)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after refusal, want false")
	}
}

// TestCloseCommand_UnresolvedDepRefused verifies that a change with an
// unarchived dependency is refused, naming the dependency, and not moved.
func TestCloseCommand_UnresolvedDepRefused(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", []string{"missing"})
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("execute() error = %q, want it to mention %q", err.Error(), "missing")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
		t.Errorf("change dir should still exist, stat err = %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading in-place onto-state.yaml: %v", err)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after refusal, want false")
	}
}

// TestCloseCommand_DirtyWorktreeRefused verifies that an uncommitted change
// in the worktree blocks close and leaves the change directory in place.
func TestCloseCommand_DirtyWorktreeRefused(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	commitAll(t, dir, "seed change")
	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "scratch.txt"), "dirty\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
		t.Errorf("change dir should still exist, stat err = %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading in-place onto-state.yaml: %v", err)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after refusal, want false")
	}
}

// assertCloseRefused runs `onto close demo --dir dir`, requires it to error
// mentioning wantSubstr, and asserts the change directory is left in place
// unarchived (nothing moved, nothing marked Archived).
func assertCloseRefused(t *testing.T, dir, wantSubstr string) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("execute() error = %q, want it to mention %q", err.Error(), wantSubstr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); statErr != nil {
		t.Errorf("change dir should still exist, stat err = %v", statErr)
	}
	st, loadErr := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if loadErr != nil {
		t.Fatalf("loading in-place onto-state.yaml: %v", loadErr)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after refusal, want false")
	}
}

// TestCloseCommand_FullRefusedWithoutPassingVerification verifies a full
// change whose verify.result is still pending is refused (even with
// close.merged and guides resolved), naming the missing verification, and
// archives nothing.
func TestCloseCommand_FullRefusedWithoutPassingVerification(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:   "demo",
		Workflow: "full",
		Phase:    "close",
		Created:  "2026-07-10",
		Verify:   ontostate.Verify{Result: "pending"},
		Close:    ontostate.Close{Merged: true},
		Guides:   "updated",
	})
	commitAll(t, dir, "seed change")

	assertCloseRefused(t, dir, "verify.result")
}

// TestCloseCommand_FullRefusedWithoutResolvedGuides verifies a full change
// with verify.result=pass and close.merged=true but guides still pending is
// refused, naming the unresolved guides, and archives nothing.
func TestCloseCommand_FullRefusedWithoutResolvedGuides(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:   "demo",
		Workflow: "full",
		Phase:    "close",
		Created:  "2026-07-10",
		Verify:   ontostate.Verify{Result: "pass"},
		Close:    ontostate.Close{Merged: true},
		Guides:   "pending",
	})
	commitAll(t, dir, "seed change")

	assertCloseRefused(t, dir, "guides")
}

// TestCloseCommand_FullRefusedWithoutMerge verifies a full change with a
// passing verification and resolved guides but close.merged=false is refused,
// naming the missing merge, and archives nothing.
func TestCloseCommand_FullRefusedWithoutMerge(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:   "demo",
		Workflow: "full",
		Phase:    "close",
		Created:  "2026-07-10",
		Verify:   ontostate.Verify{Result: "pass"},
		Close:    ontostate.Close{Merged: false},
		Guides:   "updated",
	})
	commitAll(t, dir, "seed change")

	assertCloseRefused(t, dir, "close.merged")
}

// The recorded integration choice (merge|pr) is carried through close and
// archived with the change — it never changes the close.merged gate (which
// tracks spec-delta merging, always required).
func TestCloseCommand_IntegrationChoiceCarriedThroughClose(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:         "demo",
		Workflow:       "full",
		Phase:          "close",
		Created:        "2026-07-10",
		Verify:         ontostate.Verify{Result: "pass"},
		Integration:    "pr",
		Close:          ontostate.Close{Merged: true},
		Guides:         "updated",
		CloseConfirmed: "2026-07-22 confirmed",
	})
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("close: %v; out=%s", err, out.String())
	}
	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("change not archived: %v", err)
	}
	if st.Integration != "pr" {
		t.Errorf("archived state integration = %q, want pr", st.Integration)
	}
}

// TestCloseCommand_TweakClosesWithoutGuides verifies the reduced preset gate:
// a tweak change with verify.result=pass and close.merged=true but no guides
// set satisfies the close-phase evidence gate and (with no deps and a clean
// worktree) archives.
func TestCloseCommand_TweakClosesWithoutGuides(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:         "demo",
		Workflow:       "tweak",
		Phase:          "close",
		Created:        "2026-07-10",
		Verify:         ontostate.Verify{Result: "pass"},
		Close:          ontostate.Close{Merged: true},
		CloseConfirmed: "2026-07-22 confirmed", // required for every workflow
		// Guides deliberately unset: a tweak preset does not require it.
	})
	commitAll(t, dir, "seed change")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading archived onto-state.yaml: %v", err)
	}
	if !st.Archived {
		t.Errorf("st.Archived = false, want true")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); !os.IsNotExist(err) {
		t.Errorf("original change dir stat err = %v, want IsNotExist", err)
	}
}

// TestCloseCommand_ArchiveTargetExistsRefused verifies no-clobber: if the
// dated archive target already exists, close is refused and the change
// directory is left in place.
func TestCloseCommand_ArchiveTargetExistsRefused(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	commitAll(t, dir, "seed change")

	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("pre-creating archive target: %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
		t.Errorf("change dir should still exist, stat err = %v", err)
	}

	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading in-place onto-state.yaml: %v", err)
	}
	if st.Archived {
		t.Errorf("st.Archived = true after refusal, want false")
	}
}

// TestCloseCommand_MalformedWorkflowBypassesGuides verifies that a hand-edited
// state carrying an unknown workflow value (e.g. "epic") cannot bypass the
// full-workflow guides obligation. The close command must validate the loaded
// state, not just check `workflow == "full"` — otherwise a malformed workflow
// skips the guides gate. See F9.
func TestCloseCommand_MalformedWorkflowBypassesGuides(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:   "demo",
		Workflow: "epic", // not full/fix/tweak — validation rejects this
		Phase:    "close",
		Created:  "2026-07-10",
		Verify:   ontostate.Verify{Result: "pass"},
		Close:    ontostate.Close{Merged: true},
		Guides:   "pending", // would be blocked under full; epic would skip it
	})
	commitAll(t, dir, "seed change")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err == nil {
		t.Fatalf("close must reject a state with an unknown workflow value")
	}
}

// TestCloseCommand_MalformedGuidesValueRejected verifies a guides value of
// "waived:" (empty reason) — which ValidGuides rejects but GuidesResolved
// accepts as a prefix — cannot satisfy close. Validation must run before the
// guides gate. See F9.
func TestCloseCommand_MalformedGuidesValueRejected(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change:   "demo",
		Workflow: "full",
		Phase:    "close",
		Created:  "2026-07-10",
		Verify:   ontostate.Verify{Result: "pass"},
		Close:    ontostate.Close{Merged: true},
		Guides:   "waived:", // empty reason — ValidGuides rejects, GuidesResolved accepts
	})
	commitAll(t, dir, "seed change")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err == nil {
		t.Fatalf("close must reject a guides value with an empty waived reason")
	}
}

// TestCloseCommand_RefusedWithoutCloseConfirmed: onto close refuses to archive
// until the final-confirmation gate's token is recorded, naming the setter.
func TestCloseCommand_RefusedWithoutCloseConfirmed(t *testing.T) {
	dir := prepWorkspace(t)
	name := "demo"
	seedClose(t, dir, name, nil)
	mutateState(t, dir, name, func(s *ontostate.State) { s.CloseConfirmed = "" })
	commitAll(t, dir, "seed change without token")

	_, err := runOnto(t, "close", name, "--dir", dir)
	if err == nil {
		t.Fatal("close must refuse without close_confirmed")
	}
	if !strings.Contains(err.Error(), "close-confirmed") {
		t.Errorf("error %q must name the close-confirmed setter", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docs", "changes", name)); statErr != nil {
		t.Error("refusal must leave the workspace unarchived")
	}
}
