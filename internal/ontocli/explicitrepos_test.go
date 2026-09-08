package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func explicitWorkspace(t *testing.T) workspace.Layout {
	return explicitWorkspaceMode(t, "managed")
}

func explicitWorkspaceMode(t *testing.T, mode string) workspace.Layout {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Onto Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "onto@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "Onto Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "onto@example.test")
	root := t.TempDir()
	for alias, branch := range map[string]string{"a": "main", "b": "develop"} {
		dir := filepath.Join(root, "source-"+alias)
		writeFile(t, filepath.Join(dir, "tracked"), alias+" base\n")
		runGit(t, dir, "init", "-b", branch)
		commitAll(t, dir, "base "+alias)
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version = 2\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[workflow]\nroot='records/deep/tree'\ngit=%q\n[worktrees]\ndir='execution'\n[repos]\na='source-a'\nb='source-b'\n", mode))
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0755); err != nil {
		t.Fatal(err)
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "existing" {
		if err := os.MkdirAll(l.WorkflowRoot, 0755); err != nil {
			t.Fatal(err)
		}
		records := filepath.Join(root, "records")
		runGit(t, records, "init", "-b", "records")
		writeFile(t, filepath.Join(records, "README.md"), "existing records repository\n")
		commitAll(t, records, "initialize records")
	}
	if err := workspace.InitManaged(l); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "init", "--dir", root); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestExplicitReposManagedLifecycle(t *testing.T) {
	l := explicitWorkspace(t)
	root := l.ConfigRoot
	if inGitRepository(root) {
		t.Fatal("fixture config root must not be Git")
	}
	if _, err := runOnto(t, "new", "cross", "--workflow", "tweak", "--dir", root); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Fatalf("missing selection: %v", err)
	}
	if _, err := runOnto(t, "new", "cross", "--workflow", "tweak", "--repo", "a,b", "--dir", root); err != nil {
		t.Fatal(err)
	}
	changeDir := filepath.Join(l.WorkflowRoot, "changes", "cross")
	statePath := filepath.Join(changeDir, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if st.RepoMode != "explicit" || st.BaseRef != "" || st.BaseBranch != "" || st.RepoBases["a"].BaseBranch != "main" || st.RepoBases["b"].BaseBranch != "develop" {
		t.Fatalf("bases: %+v", st)
	}
	execution := map[string]string{}
	originalHeads := map[string]string{}
	for _, alias := range st.Repos {
		originalHeads[alias], _ = resolveCommit(l.Repos[alias], "HEAD")
		writeFile(t, filepath.Join(l.Repos[alias], "tracked"), "dirty original "+alias+"\n")
		w, err := workspace.CreateWorktree(l, "onto", "cross", alias, st.RepoBases[alias].BaseBranch, "change/cross")
		if err != nil {
			t.Fatal(err)
		}
		execution[alias] = w.Path
		writeFile(t, filepath.Join(w.Path, "feature"), "feature "+alias+"\n")
		commitAll(t, w.Path, "feature "+alias)
	}
	assertOriginals := func() {
		t.Helper()
		for alias, dir := range l.Repos {
			head, _ := resolveCommit(dir, "HEAD")
			data, err := os.ReadFile(filepath.Join(dir, "tracked"))
			if head != originalHeads[alias] || err != nil || string(data) != "dirty original "+alias+"\n" {
				t.Fatalf("original %s mutated: %s %q %v", alias, head, data, err)
			}
		}
	}
	dirt, err := stateWorktreeDirt(root, st)
	if err != nil || len(dirt) != 2 || scopedDirtGateError(dirt, "cross") != "" {
		t.Fatalf("dirt: %+v %v", dirt, err)
	}
	files, _, _, err := stateDiffScale(root, st)
	if err != nil || files != 2 {
		t.Fatalf("scale files=%d: %v", files, err)
	}
	for _, field := range []string{"base-ref", "base-branch"} {
		value := st.RepoBases["a"].BaseRef
		if field == "base-branch" {
			value = "main"
		}
		if _, err := runOnto(t, "set", field, "cross", value, "--dir", root); err == nil {
			t.Fatalf("ambiguous %s accepted", field)
		}
		if _, err := runOnto(t, "set", field, "cross", value, "--repo", "a", "--dir", root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runOnto(t, "set", "base-ref", "cross", st.RepoBases["a"].BaseRef, "--repo", "b", "--dir", root); err == nil {
		t.Fatal("cross-store base accepted")
	}
	st.Phase = "verify"
	st.Integration = "merge"
	st.Verify.Scale = "light"
	if err := ontostate.Save(statePath, st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] #1 implement\n")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	if _, err := runOnto(t, "set", "verify-result", "cross", "pass", "--dir", root); err != nil {
		t.Fatal(err)
	}
	st, _ = ontostate.Load(statePath)
	if len(st.Verify.Heads) != 2 || st.Verify.Heads[""] != "" {
		t.Fatalf("verify scope: %+v", st.Verify.Heads)
	}
	writeFile(t, filepath.Join(l.WorkflowRoot, "guides", "note.md"), "records only\n")
	commitAll(t, l.WorkflowRoot, "records only")
	if err := verifyHeadsIntact(root, st); err != nil {
		t.Fatalf("records stale source: %v", err)
	}
	writeFile(t, filepath.Join(execution["b"], "feature"), "unverified source\n")
	commitAll(t, execution["b"], "unverified source")
	if err := verifyHeadsIntact(root, st); err == nil {
		t.Fatal("source movement not stale")
	}
	if _, err := runOnto(t, "set", "verify-result", "cross", "pass", "--dir", root); err != nil {
		t.Fatal(err)
	}
	st, _ = ontostate.Load(statePath)
	writeFile(t, filepath.Join(changeDir, "specs", "cross.md"), `# Delta Spec: cross

## ADDED Requirements

### Requirement: cross repository verification

Requirement-ID: REQ-cross
The system SHALL verify the selected source repositories.

#### Scenario: source verification

Scenario-ID: SC-cross
- **GIVEN** two selected source repositories
- **WHEN** verification passes
- **THEN** both source heads are recorded
`)
	if _, err := runOnto(t, "evidence", "record", "cross", "--dir", root, "--task", "1", "--scenario", "SC-cross", "--exec", "go", "--cmd-hash", cmdHash); err == nil {
		t.Fatal("ambiguous evidence accepted")
	}
	if _, err := runOnto(t, "evidence", "record", "cross", "--dir", root, "--repo", "b", "--task", "1", "--scenario", "SC-cross", "--exec", "go", "--cmd-hash", cmdHash); err != nil {
		t.Fatal(err)
	}
	sc, _, err := evidence.Load("cross", evidence.Path(changeDir))
	if err != nil || len(sc.Records) != 1 || sc.Records[0].Commit != st.Verify.Heads["b"] {
		t.Fatalf("evidence origin: %+v %v", sc, err)
	}
	if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "cross"); len(findings) != 0 {
		t.Fatalf("doctor used wrong evidence source: %v", findings)
	}
	if _, err := runOnto(t, "advance", "cross", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "set", "close-confirmed", "cross", "reviewed", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "merge-deltas", "cross", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if out, err := runOnto(t, "handoff", "cross", "--dir", root); err != nil || strings.Contains(out, "Pending decision") {
		t.Fatalf("handoff root: %s %v", out, err)
	}
	if _, err := runOnto(t, "close", "cross", "--dir", root); err != nil {
		t.Fatal(err)
	}
	assertOriginals()
	archiveDir, archived, err := locateArchive(root, "cross")
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := integrationrecord.Load(archiveDir, "cross")
	if err != nil || r.RepoMode != "explicit" || len(r.Repositories) != 2 || r.Repositories[0].BaseBranch == r.Repositories[1].BaseBranch {
		t.Fatalf("integration: %+v %v", r, err)
	}
	wrongSource := r.Repositories[0]
	wrongSource.SourceCommit = wrongSource.BaseCommit
	if err := validateIntegrationSource(root, execution[wrongSource.Alias], archived, wrongSource); err == nil {
		t.Fatal("sidecar could integrate an older, unverified source")
	}
	if ontostate.ArchiveIntegrationComplete(archiveDir, archived) || len(ontostate.DepsResolved(root, []string{"cross"})) != 1 {
		t.Fatal("pending integration resolved dependency")
	}
	if _, err := runOnto(t, "new", "cross", "--repo", "a,b", "--dir", root); err == nil {
		t.Fatal("reused pending name")
	}
	// Construct real merges without checking out or writing either original.
	receipts := map[string]string{}
	for _, entry := range r.Repositories {
		dir := execution[entry.Alias]
		tree, _ := gitOutput(t, dir, "rev-parse", entry.SourceCommit+"^{tree}")
		merge, err := gitOutput(t, dir, "commit-tree", tree, "-p", entry.BaseCommit, "-p", entry.SourceCommit, "-m", "integrate")
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "update-ref", "refs/heads/"+entry.BaseBranch, merge, entry.BaseCommit)
		receipts[entry.Alias] = "merge:" + merge
		if _, err := workspace.RemoveWorktree(l, "onto", "cross", entry.Alias, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runOnto(t, "complete-integration", "cross", "--receipt", receipts["a"], "--dir", root); err == nil {
		t.Fatal("ambiguous receipt accepted")
	}
	if _, err := runOnto(t, "complete-integration", "cross", "--repo", "b", "--receipt", receipts["a"], "--dir", root); err == nil {
		t.Fatal("wrong object store accepted")
	}
	for alias, receipt := range receipts {
		if _, err := runOnto(t, "complete-integration", "cross", "--repo", alias, "--receipt", receipt, "--dir", root); err != nil {
			t.Fatal(err)
		}
	}
	if !ontostate.ArchiveIntegrationComplete(archiveDir, archived) || len(ontostate.DepsResolved(root, []string{"cross"})) != 0 {
		t.Fatal("completed integration did not resolve dependency")
	}
	if _, err := runOnto(t, "new", "cross", "--repo", "a,b", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if len(ontostate.DepsResolved(root, []string{"cross"})) != 1 {
		t.Fatal("active name reuse resolved dependency")
	}
}

func TestExplicitSingleRepoSelection(t *testing.T) {
	l := explicitWorkspace(t)
	if _, err := runOnto(t, "new", "single", "--repo", "a", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	changeDir := filepath.Join(l.WorkflowRoot, "changes", "single")
	st, err := ontostate.LoadChange(changeDir)
	if err != nil {
		t.Fatal(err)
	}
	alias, source, err := selectedSource(l.ConfigRoot, st, "")
	if err != nil || alias != "a" || source != l.Repos["a"] {
		t.Fatalf("single source: %q %q %v", alias, source, err)
	}
	runGit(t, source, "checkout", "-b", "single")
	writeFile(t, filepath.Join(source, "feature"), "single source\n")
	commitAll(t, source, "single source")
	st.Verify.Heads, err = captureVerifyHeads(l.ConfigRoot, st)
	if err != nil {
		t.Fatal(err)
	}
	st.Verify.Result = "pass"
	st.Integration = "pr"
	entries, err := captureIntegrationEntries(l.ConfigRoot, st)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(l.WorkflowRoot, "changes", "archive", "2026-09-07-single")
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(changeDir, archive); err != nil {
		t.Fatal(err)
	}
	st.Archived, st.IntegrationRequired = true, true
	st.Phase = "close"
	if err := ontostate.Save(filepath.Join(archive, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	if err := integrationrecord.Save(archive, integrationrecord.NewExplicitPending("single", "pr", entries)); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "complete-integration", "single", "--receipt", "pr:https://example.test/pull/1", "--head", entries[0].SourceCommit, "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	if !ontostate.ArchiveIntegrationComplete(archive, st) {
		t.Fatal("unique repository receipt not completed")
	}
}

func TestExplicitSourceRejectsWrongBasesAndScope(t *testing.T) {
	l := explicitWorkspace(t)
	if _, err := runOnto(t, "new", "cross", "--repo", "a,b", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	st, _ := ontostate.Load(filepath.Join(l.WorkflowRoot, "changes", "cross", "onto-state.yaml"))
	original := st.RepoBases["a"]
	bad := original
	bad.BaseRef = st.RepoBases["b"].BaseRef
	st.RepoBases["a"] = bad
	if _, err := captureVerifyHeads(l.ConfigRoot, st); err == nil {
		t.Fatal("wrong-store base accepted")
	}
	bad = original
	bad.GitCommonDir = filepath.Join(l.ConfigRoot, "wrong", ".git")
	st.RepoBases["a"] = bad
	if _, err := stateSourceDirs(l.ConfigRoot, st); err == nil {
		t.Fatal("identity change accepted")
	}
	st.RepoBases["a"] = original
	heads, err := captureVerifyHeads(l.ConfigRoot, st)
	if err != nil {
		t.Fatal(err)
	}
	st.Verify = ontostate.Verify{Result: "pass", Heads: heads}
	delete(st.Verify.Heads, "b")
	if err := verifyHeadsIntact(l.ConfigRoot, st); err == nil {
		t.Fatal("missing verify scope accepted")
	}
	st.Verify.Heads[""] = heads["a"]
	if err := verifyHeadsIntact(l.ConfigRoot, st); err == nil {
		t.Fatal("wrong verify scope accepted")
	}
	// A config upgrade must not erase the implicit config source of old state.
	legacy := ontostate.State{Change: "old", Phase: "open", Repos: []string{"a"}}
	dirs, err := stateSourceDirs(l.ConfigRoot, legacy)
	if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": l.ConfigRoot, "a": l.Repos["a"]}) {
		t.Fatalf("legacy scope reinterpreted: %v %v", dirs, err)
	}
	if _, err := captureVerifyHeads(l.ConfigRoot, legacy); err == nil {
		t.Fatal("legacy implicit non-Git config silently omitted")
	}
	st.Verify = ontostate.Verify{}
	expected, err := captureVerifyHeads(l.ConfigRoot, st)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(l.WorkflowRoot, ".git"))
	t.Setenv("GIT_WORK_TREE", l.WorkflowRoot)
	got, err := captureVerifyHeads(l.ConfigRoot, st)
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatalf("Git environment redirected source authority: %v %v", got, err)
	}
	writeFile(t, filepath.Join(l.Repos["a"], "docs", "changes", "other", "record.md"), "source, not workflow records\n")
	dirt, err := stateWorktreeDirt(l.ConfigRoot, st)
	if err != nil || !strings.Contains(scopedDirtGateError(dirt, "cross"), "docs/changes/other/record.md") {
		t.Fatalf("false combined-workflow exception: %+v %v", dirt, err)
	}
}
