package ontocli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const reviewDelta = `# Delta Spec: sample

## ADDED Requirements

### Requirement: selected source

The system SHALL verify its selected source.

#### Scenario: source verified

- **GIVEN** a selected source
- **WHEN** verification passes
- **THEN** its HEAD is recorded
`

func reviewCloseReady(t *testing.T, l workspace.Layout, delta bool) string {
	t.Helper()
	if _, err := runOnto(t, "new", "review", "--workflow", "tweak", "--repo", "a", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	runGit(t, l.Repos["a"], "checkout", "-b", "review")
	writeFile(t, filepath.Join(l.Repos["a"], "feature"), "verified feature\n")
	commitAll(t, l.Repos["a"], "feature")
	changeDir := filepath.Join(l.WorkflowRoot, "changes", "review")
	st, err := ontostate.LoadChange(changeDir)
	if err != nil {
		t.Fatal(err)
	}
	st.Phase, st.Integration, st.CloseConfirmed = "close", "merge", "reviewed"
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] #1 implemented\n")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	if delta {
		writeFile(t, filepath.Join(changeDir, "specs", "sample.md"), reviewDelta)
	}
	if _, err := runOnto(t, "set", "verify-result", "review", "pass", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	return changeDir
}

func TestReviewRelativeCLIRecordsRoots(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "onto")
	build := exec.Command("go", "build", "-o", bin, "./cmd/onto")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build onto: %v\n%s", err, out)
	}
	for _, mode := range []string{"managed", "existing"} {
		for _, delta := range []bool{false, true} {
			for _, relative := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/delta=%t/relative-dir=%t", mode, delta, relative), func(t *testing.T) {
					l := explicitWorkspaceMode(t, mode)
					reviewCloseReady(t, l, delta)
					for _, command := range []string{"merge-deltas", "close"} {
						args := []string{command, "review"}
						cwd := l.ConfigRoot
						if relative {
							cwd = filepath.Dir(l.ConfigRoot)
							args = append(args, "--dir", filepath.Base(l.ConfigRoot))
						}
						cmd := exec.Command(bin, args...)
						cmd.Dir = cwd
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("onto %v in %s: %v\n%s", args, cwd, err, out)
						}
					}
					_, st, err := locateArchive(l.ConfigRoot, "review")
					if err != nil || !st.Archived {
						t.Fatalf("archive: %+v %v", st, err)
					}
					if delta {
						data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, "specs", "sample.md"))
						if err != nil || !strings.Contains(string(data), "selected source") {
							t.Fatalf("living spec: %q %v", data, err)
						}
					}
				})
			}
		}
	}
}

func TestReviewRecoveryValidatesPendingSource(t *testing.T) {
	l := explicitWorkspace(t)
	changeDir := reviewCloseReady(t, l, true)
	if _, err := runOnto(t, "merge-deltas", "review", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.LoadChange(changeDir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := captureIntegrationEntries(l.ConfigRoot, st)
	if err != nil {
		t.Fatal(err)
	}
	original := entries[0].SourceCommit
	entries[0].SourceCommit = entries[0].BaseCommit
	record := integrationrecord.NewExplicitPending("review", "merge", entries)
	if err := integrationrecord.Save(changeDir, record); err != nil {
		t.Fatal(err)
	}
	archive, err := archiveDestination(l.ConfigRoot, "review", "2026-09-07")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(changeDir, archive); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(archive, "onto-state.yaml")
	before, _ := os.ReadFile(statePath)
	beforeReceipt, err := os.ReadFile(integrationrecord.Path(archive))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "close", "review", "--dir", l.ConfigRoot); err == nil || !strings.Contains(err.Error(), "integration source does not contain verified source") {
		t.Fatalf("forged recovery accepted: %v", err)
	}
	after, _ := os.ReadFile(statePath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed recovery changed archive state")
	}
	afterReceipt, err := os.ReadFile(integrationrecord.Path(archive))
	if err != nil || !bytes.Equal(beforeReceipt, afterReceipt) {
		t.Fatal("failed recovery changed pending receipt")
	}
	if _, err := os.Stat(changeDir); !os.IsNotExist(err) {
		t.Fatalf("failed recovery recreated active state: %v", err)
	}
	record.Repositories[0].SourceCommit = original
	if err := integrationrecord.Save(archive, record); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "close", "review", "--dir", l.ConfigRoot); err != nil {
		t.Fatalf("valid recovery: %v", err)
	}
	st, err = ontostate.Load(statePath)
	if err != nil || !st.Archived || !st.IntegrationRequired {
		t.Fatalf("recovered archive: %+v %v", st, err)
	}
}

func TestReviewDirtNeverRefreshesSourceIndex(t *testing.T) {
	l := explicitWorkspace(t)
	if _, err := runOnto(t, "new", "index", "--repo", "a", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GIT_DIR", filepath.Join(l.WorkflowRoot, ".git"))
	env := gitAt(context.Background(), l.Repos["a"], "status").Env
	for _, guard := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"} {
		found := 0
		key := strings.SplitN(guard, "=", 2)[0] + "="
		for _, value := range env {
			if strings.HasPrefix(value, key) {
				if value != guard {
					t.Fatalf("unsafe Git guard: %s", value)
				}
				found++
			}
		}
		if found != 1 {
			t.Fatalf("missing or repeated Git guard %s: %v", guard, env)
		}
	}
	index := filepath.Join(l.Repos["a"], ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(l.Repos["a"], "tracked"), now, now); err != nil {
		t.Fatal(err)
	}
	if out, err := runOnto(t, "dirt", "index", "--json", "--dir", l.ConfigRoot); err != nil || !strings.Contains(out, `"clean": true`) {
		t.Fatalf("dirt: %s %v", out, err)
	}
	after, err := os.ReadFile(index)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only dirt refreshed original index: %v", err)
	}
}

func TestReviewHandoffCarriesEveryBoundSource(t *testing.T) {
	l := explicitWorkspace(t)
	if _, err := runOnto(t, "new", "pack", "--workflow", "tweak", "--repo", "a,b", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	changeDir := filepath.Join(l.WorkflowRoot, "changes", "pack")
	st, err := ontostate.LoadChange(changeDir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{}
	for _, alias := range st.Repos {
		w, err := workspace.CreateWorktree(l, "onto", "pack", alias, st.RepoBases[alias].BaseBranch, "work/pack")
		if err != nil {
			t.Fatal(err)
		}
		dirs[alias] = w.Path
	}
	if _, err := runOnto(t, "handoff", "pack", "--write", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(changeDir, ".onto", "handoff", "*-context.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("pack paths: %v %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var rec handoff.Recovery
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.HeadCommit != "" || rec.BaseRef != "" || len(rec.Sources) != 2 {
		t.Fatalf("lost multi-source context: %+v", rec)
	}
	for alias, source := range rec.Sources {
		base := st.RepoBases[alias]
		head, err := resolveCommit(dirs[alias], "HEAD")
		if err != nil || source.Dir != dirs[alias] || source.HeadCommit != head || source.BaseRef != base.BaseRef || source.BaseBranch != base.BaseBranch || source.GitCommonDir != base.GitCommonDir {
			t.Fatalf("source %s: %+v, %v", alias, source, err)
		}
	}
	if n := len(rec.NextArgv); n < 2 || !reflect.DeepEqual(rec.NextArgv[n-2:], []string{"--dir", l.ConfigRoot}) {
		t.Fatalf("unqualified next invocation: %v", rec.NextArgv)
	}
	if _, err := runOnto(t, rec.NextArgv[1:]...); err != nil {
		t.Fatalf("recovery command from outside config root: %v", err)
	}
	st.Workflow = "full"
	gated, err := buildRecovery(NewRootCmd(), l.ConfigRoot, "pack", changeDir, st)
	if err != nil || len(gated.PendingGates) == 0 {
		t.Fatalf("gated recovery: %+v %v", gated, err)
	}
	for _, gate := range gated.PendingGates {
		if n := len(gate.SetArgv); n < 2 || !reflect.DeepEqual(gate.SetArgv[n-2:], []string{"--dir", l.ConfigRoot}) {
			t.Fatalf("unqualified gate: %+v", gate)
		}
	}
	if !reflect.DeepEqual(gated.NextArgv, gated.PendingGates[0].SetArgv) {
		t.Fatal("next gate invocation lost qualification")
	}
	markdown, err := os.ReadFile(strings.TrimSuffix(paths[0], ".json") + ".md")
	if err != nil || !strings.Contains(string(markdown), dirs["b"]) || !strings.Contains(string(markdown), st.RepoBases["b"].BaseBranch) {
		t.Fatalf("markdown source context: %s %v", markdown, err)
	}
	// A valid first binding must not hide a corrupt second binding.
	runGit(t, dirs["b"], "checkout", "--detach")
	if _, err := runOnto(t, "handoff", "pack", "--write", "--dir", l.ConfigRoot); err == nil {
		t.Fatal("handoff accepted invalid second binding")
	}
	after, _ := filepath.Glob(filepath.Join(changeDir, ".onto", "handoff", "*-context.json"))
	if len(after) != 1 {
		t.Fatal("invalid handoff wrote a recovery envelope")
	}
}

func TestReviewNewExplicitBaseOverrides(t *testing.T) {
	l := explicitWorkspace(t)
	source := l.Repos["a"]
	base, _ := resolveCommit(source, "HEAD")
	runGit(t, source, "checkout", "-b", "unrelated-feature")
	writeFile(t, filepath.Join(source, "feature"), "unrelated work\n")
	commitAll(t, source, "unrelated feature")
	writeFile(t, filepath.Join(source, "tracked"), "dirty feature\n")
	head, _ := resolveCommit(source, "HEAD")
	index, _ := os.ReadFile(filepath.Join(source, ".git", "index"))
	if _, err := runOnto(t, "new", "from-main", "--repo", "a,b", "--base", "a=refs/heads/main", "--base", "b=develop", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.LoadChange(filepath.Join(l.WorkflowRoot, "changes", "from-main"))
	if err != nil || st.RepoBases["a"].BaseRef != base || st.RepoBases["a"].BaseBranch != "main" || st.RepoBases["b"].BaseBranch != "develop" {
		t.Fatalf("explicit bases: %+v %v", st, err)
	}
	if _, err := workspace.CreateWorktree(l, "onto", "from-main", "a", "main", "work/from-main"); err != nil {
		t.Fatal(err)
	}
	afterHead, _ := resolveCommit(source, "HEAD")
	afterIndex, _ := os.ReadFile(filepath.Join(source, ".git", "index"))
	data, _ := os.ReadFile(filepath.Join(source, "tracked"))
	if afterHead != head || !bytes.Equal(index, afterIndex) || string(data) != "dirty feature\n" {
		t.Fatal("base selection mutated the original feature checkout")
	}
	for i, bases := range [][]string{{"a=main", "a=main"}, {"missing=main"}, {"b=develop"}, {"a="}, {"a=HEAD"}, {"a=main~1"}, {"a=--help"}, {"a=refs/tags/main"}, {"a=missing"}, {"main"}} {
		name := fmt.Sprintf("bad-%d", i)
		args := []string{"new", name, "--repo", "a", "--dir", l.ConfigRoot}
		for _, base := range bases {
			args = append(args, "--base", base)
		}
		if _, err := runOnto(t, args...); err == nil {
			t.Fatalf("accepted bases %v", bases)
		}
		if _, err := os.Stat(filepath.Join(l.WorkflowRoot, "changes", name)); !os.IsNotExist(err) {
			t.Fatalf("invalid base created state: %v", err)
		}
	}
	legacy := prepWorkspace(t)
	if _, err := runOnto(t, "new", "bad", "--base", "a=main", "--dir", legacy); err == nil {
		t.Fatal("legacy accepted --base")
	}
}

func TestReviewNewRefusesRetainedRegistryGeneration(t *testing.T) {
	l := explicitWorkspace(t)
	if _, err := runOnto(t, "new", "retained", "--repo", "a", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.CreateWorktree(l, "onto", "retained", "a", "main", "work/retained"); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(l.WorkflowRoot, "changes", "retained")
	st, err := ontostate.LoadChange(active)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(l.WorkflowRoot, "changes", "archive", "2026-09-07-retained")
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(active, archive); err != nil {
		t.Fatal(err)
	}
	st.Archived = true
	if err := ontostate.Save(filepath.Join(archive, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(archive, "onto-state.yaml"))
	if _, err := runOnto(t, "new", "retained", "--repo", "a", "--dir", l.ConfigRoot); err == nil || !strings.Contains(err.Error(), "before reusing") {
		t.Fatalf("retained registry reused: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(archive, "onto-state.yaml"))
	if !bytes.Equal(before, after) {
		t.Fatal("name guard modified archive")
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("name guard wrote new state: %v", err)
	}
}

func TestReviewLegacyEmptyScopeIgnoresUnavailableRepos(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := prepWorkspace(t)
			writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version=%d\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[repos]\nunavailable='/missing/source-repo'\n", version))
			if _, err := runOnto(t, "new", "local", "--dir", root); err != nil {
				t.Fatal(err)
			}
			st, err := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "local"))
			if err != nil {
				t.Fatal(err)
			}
			dirs, err := stateSourceDirs(root, st)
			if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": root}) {
				t.Fatalf("unrelated repo probed: %v %v", dirs, err)
			}
			if _, err := runOnto(t, "new", "selected", "--repo", "unavailable", "--dir", root); err == nil {
				t.Fatal("unavailable selected repository accepted")
			}
		})
	}
	for _, config := range []string{"schema_version=2\n[repos]\na='/missing/source-repo'\n", "schema_version=3\n", "schema_version=-1\n", "schema_version='invalid'\n", "[workflow\n"} {
		root := prepWorkspace(t)
		writeFile(t, filepath.Join(root, "homonto.toml"), config)
		if _, err := runOnto(t, "new", "bad", "--dir", root); err == nil {
			t.Fatalf("invalid config fell back to legacy: %s", config)
		}
		if _, err := stateSourceDirs(root, ontostate.State{Change: "old", Phase: "open"}); err == nil {
			t.Fatalf("invalid source config fell back to legacy: %s", config)
		}
	}
}
