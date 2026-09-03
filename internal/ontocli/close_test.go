package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
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
		content := ""
		if f == "verification.md" {
			content = "Result: pass\n"
		}
		writeFile(t, filepath.Join(changeDir, f), content)
	}
	if st.Close.Merged {
		if err := saveMergeReceipt(changeDir, mergeReceipt{Change: st.Change, Entries: []mergeReceiptEntry{}}); err != nil {
			t.Fatalf("seedCloseState: saving empty merge receipt: %v", err)
		}
	}
}

// seedClose writes a full-workflow change at "close" with the given deps and
// all close-phase evidence resolved (verify.result=pass, close.merged=true,
// guides=updated), so the close-phase evidence gates are satisfied and the
// existing structural gates (deps/dirty/no-clobber) are what remain under
// test. Callers exercising the evidence gates themselves use seedCloseState.
func seedClose(t *testing.T, root, name string, deps []string) {
	t.Helper()
	baseBranch, err := gitOutput(t, root, "branch", "--show-current")
	if err != nil || baseBranch == "" {
		t.Fatalf("seedClose: determining base branch: %v (%q)", err, baseBranch)
	}
	seedCloseState(t, root, ontostate.State{
		Change:         name,
		Workflow:       "full",
		Phase:          "close",
		Created:        "2026-07-10",
		BaseRef:        "abc123",
		BaseBranch:     baseBranch,
		Deps:           deps,
		Verify:         ontostate.Verify{Result: "pass"},
		Close:          ontostate.Close{Merged: true},
		Guides:         "updated",
		Integration:    "merge",
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
	checkoutChangeBranch(t, dir, "demo")
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
	if !st.IntegrationRequired {
		t.Errorf("st.IntegrationRequired = false, want true")
	}
	if st.Phase != "close" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "close")
	}
	integration, ok, err := integrationrecord.Load(archiveDir, "demo")
	if err != nil || !ok || integration.Status != integrationrecord.StatusPending {
		t.Fatalf("archived integration record = %+v, %v, present=%v; want pending", integration, err, ok)
	}
	if got := ontostate.DeriveWorkingPhase(archiveDir, st); got != "close" {
		t.Errorf("pending integration derived phase = %q, want close", got)
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); !os.IsNotExist(err) {
		t.Errorf("original change dir stat err = %v, want IsNotExist", err)
	}
}

func TestCloseCommand_RecoversInterruptedMove(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	commitAll(t, dir, "seed change")
	changeDir := filepath.Join(dir, "docs", "changes", "demo")
	record := integrationrecord.NewPending("demo", "merge", seedBaseBranch(t, dir), []integrationrecord.Entry{syntheticEntry(t, dir, "")})
	if err := integrationrecord.Save(changeDir, record); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(changeDir, archiveDir); err != nil {
		t.Fatal(err)
	}

	out, err := runOnto(t, "close", "demo", "--dir", dir)
	if err != nil {
		t.Fatalf("recover close: %v", err)
	}
	if !strings.Contains(out, "recovered interrupted archive") {
		t.Fatalf("recovery output = %q", out)
	}
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil || !st.Archived {
		t.Fatalf("recovered state = %+v, %v", st, err)
	}
}

func TestCloseCommand_RecoveryRejectsDirtySelectedRepo(t *testing.T) {
	root := prepWorkspace(t)
	api := filepath.Join(filepath.Dir(root), "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, api, "init")
	runGit(t, api, "config", "user.email", "test@example.com")
	runGit(t, api, "config", "user.name", "Test")
	writeFile(t, filepath.Join(api, "tracked"), "clean\n")
	commitAll(t, api, "init")
	writeFile(t, filepath.Join(root, "homonto.toml"), `[frameworks.onto]
source="builtin:onto"
scope="project"
[subagents.onto.opencode]
model="test/model"
[subagents.onto-explorer.opencode]
model="test/model"
[subagents.onto-reviewer.opencode]
model="test/model"
[subagents.onto-implementer.opencode]
model="test/model"
[subagents.onto-skeptic.opencode]
model="test/model"
[repos]
api="../api"
`)
	seedCloseState(t, root, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "close", BaseRef: "abc123", BaseBranch: seedBaseBranch(t, root), Repos: []string{"api"},
		Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true}, Guides: "updated",
		Integration: "merge", CloseConfirmed: "reviewed",
	})
	commitAll(t, root, "seed change")
	changeDir := filepath.Join(root, "docs", "changes", "demo")
	if err := integrationrecord.Save(changeDir, integrationrecord.NewPending("demo", "merge", seedBaseBranch(t, root), []integrationrecord.Entry{syntheticEntry(t, root, "")})); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(root, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(changeDir, archiveDir); err != nil {
		t.Fatal(err)
	}
	dirtyWorktree(t, api)

	if _, err := runOnto(t, "close", "demo", "--dir", root); err == nil || !strings.Contains(err.Error(), "api:") {
		t.Fatalf("recovery did not reject selected-repo dirt: %v", err)
	}
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil || st.Archived {
		t.Fatalf("recovery changed archived state after refusal: %+v, %v", st, err)
	}
}

func TestCloseCommand_RefusesSymlinkedArchiveParent(t *testing.T) {
	root := prepWorkspace(t)
	seedClose(t, root, "demo", nil)
	commitAll(t, root, "seed change")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "docs", "changes", "archive")); err != nil {
		t.Fatal(err)
	}

	if _, err := runOnto(t, "close", "demo", "--dir", root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("close accepted symlinked archive parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "changes", "demo")); err != nil {
		t.Fatalf("change moved despite unsafe archive path: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("archive escaped root: entries=%v err=%v", entries, err)
	}
}

func TestCloseCommand_IntegrationDirtFilterRetainsRenameSource(t *testing.T) {
	root := prepWorkspace(t)
	seedClose(t, root, "demo", nil)
	base := seedBaseBranch(t, root)
	recordJSON := `{"schemaVersion":2,"change":"demo","mode":"merge","baseBranch":"` + base + `","status":"pending","repositories":[{"baseBranch":"` + base + `","baseCommit":"1111111111111111111111111111111111111111","sourceBranch":"change/demo","sourceCommit":"2222222222222222222222222222222222222222"}]}`
	writeFile(t, filepath.Join(root, "tracked-integration.json"), recordJSON)
	commitAll(t, root, "seed change and source")
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "demo", ".onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "mv", "tracked-integration.json", "docs/changes/demo/.onto/integration.json")

	if _, err := runOnto(t, "close", "demo", "--dir", root); err == nil || !strings.Contains(err.Error(), "tracked-integration.json") {
		t.Fatalf("close hid renamed source deletion: %v", err)
	}
}

// TestCloseCommand_RefusesSourceCommitsAfterVerification pins the verify-head
// binding: a source commit landing after `set verify-result pass` refuses
// close until the change is re-verified; a docs-only bookkeeping commit is
// still fine, and re-recording the pass rebinds to the new HEAD.
func TestCloseCommand_RefusesSourceCommitsAfterVerification(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	if _, err := runOnto(t, "set", "verify-result", "demo", "pass", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "main.go"), "package main // unverified edit\n")
	commitAll(t, dir, "post-verification source change")
	checkoutChangeBranch(t, dir, "demo")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "re-verify") {
		t.Fatalf("close accepted a post-verification source commit: %v", err)
	}

	// Re-verification at the new HEAD rebinds and unblocks the close. The
	// re-recorded state file is itself bookkeeping and may be committed.
	if _, err := runOnto(t, "set", "verify-result", "demo", "pass", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "record re-verification")
	if _, err := runOnto(t, "close", "demo", "--dir", dir); err != nil {
		t.Fatalf("close after re-verification: %v", err)
	}
}

// TestCloseCommand_AllowsBookkeepingAfterVerification: workflow bookkeeping
// (guides/state/spec deltas under docs/) after the recorded pass does not
// block close.
func TestCloseCommand_AllowsBookkeepingAfterVerification(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	if _, err := runOnto(t, "set", "verify-result", "demo", "pass", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "docs", "guides", "example.md"), "guide update\n")
	commitAll(t, dir, "guides bookkeeping")
	checkoutChangeBranch(t, dir, "demo")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err != nil {
		t.Fatalf("bookkeeping after verification must not block close: %v", err)
	}
}

// TestCloseCommand_VerifyHeadsRefuseRenamedSource: --no-renames in the
// verification diff keeps a source file moved into docs/ from hiding its
// deleted origin (the same class the dirt scan closes).
func TestCloseCommand_VerifyHeadsRefuseRenamedSource(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	writeFile(t, filepath.Join(dir, "internal", "thing.go"), "package internal\n")
	commitAll(t, dir, "seed a source file")
	if _, err := runOnto(t, "set", "verify-result", "demo", "pass", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs", "guides"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "mv", "internal/thing.go", "docs/guides/thing.md")
	commitAll(t, dir, "move source into docs")
	checkoutChangeBranch(t, dir, "demo")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "internal/thing.go") {
		t.Fatalf("rename into docs hid the deleted source path: %v", err)
	}
}

// TestCloseCommand_VerifyHeadsRefuseAliasedHead: a hand-shaped head naming a
// moving rev re-binds itself at close; only canonical commit ids are valid.
func TestCloseCommand_VerifyHeadsRefuseAliasedHead(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	if _, err := runOnto(t, "set", "verify-result", "demo", "pass", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "main.go"), "package main // unverified\n")
	commitAll(t, dir, "post-verification source change")
	// Hand-shape the head into a moving rev — exactly the surgery the shape
	// check exists to refuse.
	mutateState(t, dir, "demo", func(st *ontostate.State) {
		st.Verify.Heads = map[string]string{"": "HEAD"}
	})
	checkoutChangeBranch(t, dir, "demo")

	if _, err := runOnto(t, "close", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "canonical commit id") {
		t.Fatalf("aliased verification head accepted: %v", err)
	}
}

// TestCloseCommand_SiblingDocsChangesDirtBlocks: the config repo's foreign-
// change carve-out must not excuse dirt under a selected sibling's own
// docs/changes/ — external repositories have no central workflow tree.
func TestCloseCommand_SiblingDocsChangesDirtBlocks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "config")
	api := filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@example.com")
		runGit(t, dir, "config", "user.name", "Test")
		writeFile(t, filepath.Join(dir, "tracked"), "ok\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-m", "init")
	}
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), `[frameworks.onto]
source="builtin:onto"
scope="project"
[subagents.onto.opencode]
model="test/model"
[subagents.onto-explorer.opencode]
model="test/model"
[subagents.onto-reviewer.opencode]
model="test/model"
[subagents.onto-implementer.opencode]
model="test/model"
[subagents.onto-skeptic.opencode]
model="test/model"
[repos]
api="../api"
`)
	seedCloseState(t, root, ontostate.State{
		Change: "cross", Workflow: "full", Phase: "close", BaseRef: "abc123", BaseBranch: seedBaseBranch(t, root), Repos: []string{"api"},
		Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true}, Guides: "updated",
		Integration: "merge", CloseConfirmed: "reviewed",
	})
	commitAll(t, root, "seed config")
	runGit(t, api, "checkout", "-q", "-b", "change/cross")
	writeFile(t, filepath.Join(api, "docs", "changes", "stray", "note.md"), "sibling workflow dirt\n")

	if _, err := runOnto(t, "close", "cross", "--dir", root); err == nil || !strings.Contains(err.Error(), "docs/changes/stray") {
		t.Fatalf("sibling docs/changes dirt did not block close: %v", err)
	}
}

// TestSetVerifyResult_LoudCaptureFailures: inside git, a broken repository
// scope refuses the pass instead of recording it unbound; outside git the
// pass is refused outright (there is nothing to bind).
func TestSetVerifyResult_LoudCaptureFailures(t *testing.T) {
	// Outside git: refused.
	plain := setUpGatedWorkspace(t)
	seedChange(t, plain, "c", "verify")
	if _, err := runOnto(t, "set", "verify-result", "c", "pass", "--dir", plain); err == nil {
		t.Fatal("pass recorded outside a git repository")
	}

	// Inside git with a broken scope: refused, naming the scope failure.
	base := t.TempDir()
	root := filepath.Join(base, "config")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	writeFile(t, filepath.Join(root, "tracked"), "ok\n")
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "init")
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), `[frameworks.onto]
source="builtin:onto"
scope="project"
[subagents.onto.opencode]
model="test/model"
[subagents.onto-explorer.opencode]
model="test/model"
[subagents.onto-reviewer.opencode]
model="test/model"
[subagents.onto-implementer.opencode]
model="test/model"
[subagents.onto-skeptic.opencode]
model="test/model"
`)
	seedChange(t, root, "c", "verify")
	if _, err := runOnto(t, "set", "verify-result", "c", "pass", "--dir", root); err != nil {
		t.Fatalf("in-git capture without repos must succeed: %v", err)
	}
	// With a declared-but-missing alias the capture fails loudly.
	if err := os.MkdirAll(filepath.Join(base, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Join(base, "api"), "init")
	writeFile(t, filepath.Join(root, "homonto.toml"), `[frameworks.onto]
source="builtin:onto"
scope="project"
[subagents.onto.opencode]
model="test/model"
[repos]
ghost="../gone"
`)
	mutateState(t, root, "c", func(st *ontostate.State) { st.Repos = []string{"ghost"} })
	if _, err := runOnto(t, "set", "verify-result", "c", "pass", "--dir", root); err == nil || !strings.Contains(err.Error(), "bind the pass") {
		t.Fatalf("broken scope recorded an unbound pass: %v", err)
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
		Change:         "demo",
		Workflow:       "full",
		Phase:          "close",
		Created:        "2026-07-10",
		BaseRef:        "abc123",
		BaseBranch:     "main",
		Verify:         ontostate.Verify{Result: "pass"},
		Close:          ontostate.Close{Merged: false},
		Guides:         "updated",
		Integration:    "merge",
		CloseConfirmed: "reviewed",
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
		BaseRef:        "abc123",
		BaseBranch:     seedBaseBranch(t, dir),
		Verify:         ontostate.Verify{Result: "pass"},
		Integration:    "pr",
		Close:          ontostate.Close{Merged: true},
		Guides:         "updated",
		CloseConfirmed: "2026-07-22 confirmed",
	})
	checkoutChangeBranch(t, dir, "demo")
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
		BaseRef:        "abc123",
		BaseBranch:     seedBaseBranch(t, dir),
		Verify:         ontostate.Verify{Result: "pass"},
		Close:          ontostate.Close{Merged: true},
		Integration:    "merge",
		CloseConfirmed: "2026-07-22 confirmed", // required for every workflow
		// Guides deliberately unset: a tweak preset does not require it.
	})
	checkoutChangeBranch(t, dir, "demo")
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

func TestCloseCommand_ArchiveTargetExistsUsesSameDaySuffix(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	checkoutChangeBranch(t, dir, "demo")
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

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute() = %v, want suffixed archive", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); !os.IsNotExist(err) {
		t.Errorf("change dir still exists, stat err = %v", err)
	}
	st, err := ontostate.Load(filepath.Join(archiveDir+"-2", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading suffixed archive state: %v", err)
	}
	if !st.Archived {
		t.Errorf("st.Archived = false, want true")
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

func TestCloseCommand_RefusedWithoutIntegration(t *testing.T) {
	dir := prepWorkspace(t)
	name := "demo"
	seedClose(t, dir, name, nil)
	mutateState(t, dir, name, func(s *ontostate.State) { s.Integration = "" })
	commitAll(t, dir, "seed change without integration")

	_, err := runOnto(t, "close", name, "--dir", dir)
	if err == nil || !strings.Contains(err.Error(), "set integration") {
		t.Fatalf("close must refuse without integration, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "docs", "changes", name)); statErr != nil {
		t.Error("refusal must leave the workspace unarchived")
	}
}

func TestCloseCommand_RefusedWithoutGitAnchors(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*ontostate.State)
	}{
		{name: "base ref", want: "base_ref", mutate: func(s *ontostate.State) { s.BaseRef = "" }},
		{name: "base branch", want: "base_branch", mutate: func(s *ontostate.State) { s.BaseBranch = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedClose(t, dir, "demo", nil)
			mutateState(t, dir, "demo", tc.mutate)
			commitAll(t, dir, "seed missing anchor")
			assertCloseRefused(t, dir, tc.want)
		})
	}
}

func TestCloseCommand_RequiresCumulativeArtifacts(t *testing.T) {
	for _, artifact := range []string{"proposal.md", "tasks.md", "design.md", "plan.md", "verification.md"} {
		t.Run(artifact, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedClose(t, dir, "demo", nil)
			if err := os.Remove(filepath.Join(dir, "docs", "changes", "demo", artifact)); err != nil {
				t.Fatal(err)
			}
			commitAll(t, dir, "seed missing artifact")
			assertCloseRefused(t, dir, artifact)
		})
	}
}

func TestCloseCommand_RefusedWhenVerificationReportDisagrees(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{name: "missing result", content: "# Verification\n", want: "exactly one canonical"},
		{name: "failed result", content: "Result: fail\n", want: "verification.md says"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedClose(t, dir, "demo", nil)
			writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "verification.md"), tc.content)
			commitAll(t, dir, "seed contradictory verification")
			assertCloseRefused(t, dir, tc.want)
		})
	}
}

func TestCloseCommand_RefusedWhenRecordedMergeIsStale(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "specs", "cap.md"),
		"## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	commitAll(t, dir, "seed stale merged marker")

	assertCloseRefused(t, dir, "merge receipt")
}

// TestCloseCommand_RefusedWithoutCloseConfirmed: onto close refuses to archive
// until the close-plan review token is recorded, naming the setter.
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
