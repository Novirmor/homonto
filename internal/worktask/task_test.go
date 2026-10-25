package worktask

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func fixture(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	for _, alias := range []string{"api", "web"} {
		dir := filepath.Join(root, alias)
		put(t, filepath.Join(dir, "tracked"), "original\n")
		runGit(t, dir, "init", "-b", "main")
		runGit(t, dir, "config", "user.name", "Test")
		runGit(t, dir, "config", "user.email", "test@example.invalid")
		runGit(t, dir, "config", "commit.gpgsign", "false")
		put(t, filepath.Join(dir, "check.sh"), "set -eu\ntest -f tracked\ncat \"$HOMONTO_INTEGRATION_CONTEXT\"\npwd\n")
		runGit(t, dir, "add", ".")
		runGit(t, dir, "commit", "-m", "base")
	}
	text := "schema_version=2\n[repos]\napi='../api'\nweb='../web'\n[workflow]\nroot='../records'\n[worktrees]\ndir='../allocated'\n[integration.pair]\nrepos=['api','web']\nrepo='api'\ncwd='.'\ncommand=['sh','check.sh']\n"
	for _, engine := range []string{"onto", "to", "no"} {
		text += fmt.Sprintf("[engines.%s]\nsource='builtin:%s'\nscope='project'\n", engine, engine)
	}
	for _, agent := range []string{"homonto", "onto-explorer", "onto-implementer", "onto-reviewer", "onto-skeptic", "to-explorer", "to-implementer", "to-reviewer", "to-skeptic", "no-implementer", "no-reviewer"} {
		text += fmt.Sprintf("[subagents.%s.opencode]\nmodel='test/model'\n", agent)
	}
	path := filepath.Join(root, "control", "homonto.toml")
	put(t, path, text)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createNo(t *testing.T, s *Store, mode string) *Task {
	t.Helper()
	task, err := s.Create(context.Background(), "feature", "no", "", mode, []string{"api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCheckUsesAllocatedCandidatesAndInvalidatesEvidence(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "worktree")
	ctx := context.Background()
	r, err := s.RunCheck(ctx, task, "pair", nil)
	if err != nil || r.Status != "passed" || r.ExitCode != 0 {
		t.Fatalf("run = %+v, %v", r, err)
	}
	var got CheckContext
	if err := readJSON(r.Context, &got); err != nil {
		t.Fatal(err)
	}
	for alias, input := range got.Repos {
		if input.Path != task.Checkouts[alias].Path || input.Path == s.Layout.Repos[alias] || input.Revision != runGit(t, input.Path, "rev-parse", "HEAD") {
			t.Fatalf("wrong input: %+v", input)
		}
	}
	out, err := os.ReadFile(r.Output)
	if err != nil || !strings.Contains(string(out), task.Checkouts["api"].Path) {
		t.Fatalf("output = %s, %v", out, err)
	}
	s.RefreshResults(ctx, task)
	if task.Results["pair"].Stale {
		t.Fatal("fresh evidence marked stale")
	}
	api := task.Checkouts["api"].Path
	put(t, filepath.Join(api, "tracked"), "candidate\n")
	s.RefreshResults(ctx, task)
	if !task.Results["pair"].Stale {
		t.Fatal("dirty candidate retained passing evidence")
	}
	runGit(t, api, "add", "tracked")
	runGit(t, api, "commit", "-m", "candidate")
	s.RefreshResults(ctx, task)
	if !task.Results["pair"].Stale {
		t.Fatal("new commit retained passing evidence")
	}
	if _, err := s.RunCheck(ctx, task, "pair", nil); err != nil {
		t.Fatal(err)
	}
	check := s.Config.Integration["pair"]
	check.Command = []string{"sh", "-c", "echo failure; exit 7"}
	s.Config.Integration["pair"] = check
	s.RefreshResults(ctx, task)
	if !task.Results["pair"].Stale {
		t.Fatal("changed command retained evidence")
	}
	r, err = s.RunCheck(ctx, task, "pair", nil)
	if err == nil || r.Status != "failed" || r.ExitCode != 7 {
		t.Fatalf("failure = %+v, %v", r, err)
	}
	loaded, err := s.Load(task.Name)
	if err != nil || loaded.Results["pair"].Status != "failed" {
		t.Fatalf("failure not persisted: %+v, %v", loaded, err)
	}
	if task.NeedsFallback {
		t.Fatal("configured failure allowed fallback")
	}
}

func TestResumeEngineIsolationAndCleanup(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "worktree")
	ctx := context.Background()
	resumed, err := s.Create(ctx, task.Name, "no", "", "worktree", []string{"api"}, nil)
	if err != nil || resumed.Checkouts["api"] != task.Checkouts["api"] {
		t.Fatalf("resume = %+v, %v", resumed, err)
	}
	for _, engine := range []string{"onto", "to"} {
		if _, err := s.Create(ctx, task.Name, engine, "", "worktree", []string{"api"}, nil); err == nil {
			t.Fatal("changed selected engine")
		}
	}
	if _, err := s.Create(ctx, task.Name, "no", "", "existing", []string{"api"}, nil); err == nil {
		t.Fatal("changed isolation")
	}
	api := task.Checkouts["api"].Path
	put(t, filepath.Join(api, "tracked"), "candidate\n")
	if err := s.Cleanup(ctx, task, true); err == nil {
		t.Fatal("removed dirty candidate")
	}
	runGit(t, api, "add", ".")
	runGit(t, api, "commit", "-m", "candidate")
	if err := s.Cleanup(ctx, task, true); err == nil {
		t.Fatal("removed unintegrated candidate")
	}
	runGit(t, s.Layout.Repos["api"], "merge", "--ff-only", task.Checkouts["api"].Branch)
	if err := s.Cleanup(ctx, task, false); err == nil {
		t.Fatal("cleanup without confirmation")
	}
	if err := s.Cleanup(ctx, task, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(task.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(api); !os.IsNotExist(err) {
		t.Fatalf("owned checkout remains: %v", err)
	}
	for _, path := range []string{s.Layout.WorkflowRoot, filepath.Join(s.Layout.ConfigRoot, ".git")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("no created lifecycle/config Git state: %s", path)
		}
	}
}

func TestDirtyVerificationInputNeedsExplicitDisposition(t *testing.T) {
	s := fixture(t)
	web := s.Layout.Repos["web"]
	put(t, filepath.Join(web, "tracked"), "user work\n")
	ctx := context.Background()
	if _, err := s.Create(ctx, "feature", "no", "", "worktree", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "disposition") {
		t.Fatalf("dirty verification input: %v", err)
	}
	task, err := s.Create(ctx, "feature", "no", "", "worktree", []string{"api"}, map[string]string{"web": "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCheck(ctx, task, "pair", nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(web, "tracked"))
	if err != nil || string(data) != "user work\n" {
		t.Fatal("original dirt was modified")
	}
}

func TestCleanupPreservesMovedVerificationCheckout(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	task, err := s.Create(ctx, "feature", "no", "", "existing", []string{"api"}, map[string]string{"web": "worktree"})
	if err != nil {
		t.Fatal(err)
	}
	web := task.Checkouts["web"].Path
	// Verification inputs are detached. A new clean commit has no branch keeping
	// it reachable once its worktree is removed.
	runGit(t, web, "commit", "--allow-empty", "-m", "work to preserve")
	head := runGit(t, web, "rev-parse", "HEAD")
	if _, err := s.RunCheck(ctx, task, "pair", nil); err == nil {
		t.Fatal("moved verification input accepted")
	}
	if err := s.Cleanup(ctx, task, true); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("cleanup of moved verification input = %v, want pinned-revision refusal", err)
	}
	if got := runGit(t, web, "rev-parse", "HEAD"); got != head {
		t.Fatalf("verification checkout changed: got %s, want %s", got, head)
	}
	resumed, err := s.Load(task.Name)
	if err != nil || resumed.Checkouts["web"].Removed {
		t.Fatalf("refused cleanup lost checkout metadata: %+v, %v", resumed, err)
	}
}

func TestPinnedInputsMutationAndCwdEscape(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "existing")
	ctx := context.Background()
	web := s.Layout.Repos["web"]
	runGit(t, web, "commit", "--allow-empty", "-m", "moved")
	if _, err := s.RunCheck(ctx, task, "pair", nil); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("moved pinned input: %v", err)
	}
	// A fresh task pins the new committed input.
	task, err := s.Create(ctx, "new", "no", "", "existing", []string{"api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := s.Config.Integration["pair"]
	check.Command = []string{"sh", "-c", "echo edited > tracked"}
	s.Config.Integration["pair"] = check
	r, err := s.RunCheck(ctx, task, "pair", nil)
	if err == nil || r.Status != "failed" || !r.Stale {
		t.Fatalf("mutating check = %+v, %v", r, err)
	}
	api := s.Layout.Repos["api"]
	runGit(t, api, "add", ".")
	runGit(t, api, "commit", "-m", "edited")
	if err := os.Symlink(t.TempDir(), filepath.Join(api, "escape")); err != nil {
		t.Fatal(err)
	}
	runGit(t, api, "add", ".")
	runGit(t, api, "commit", "-m", "symlink")
	check.Cwd = "escape"
	s.Config.Integration["pair"] = check
	if _, err := s.RunCheck(ctx, task, "pair", nil); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("cwd escape: %v", err)
	}
}

func TestNativeEnginesReuseRecordedBasesAndBindings(t *testing.T) {
	for _, engine := range []string{"onto", "to"} {
		t.Run(engine, func(t *testing.T) {
			s := fixture(t)
			api := s.Layout.Repos["api"]
			base := runGit(t, api, "rev-parse", "HEAD")
			common := runGit(t, api, "rev-parse", "--absolute-git-dir")
			// The original may be on another branch than the frozen target.
			runGit(t, api, "checkout", "-b", "unrelated")
			runGit(t, api, "commit", "--allow-empty", "-m", "unrelated")
			if err := workflowroot.WriteLayoutMarker(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: s.Layout.ConfigPath, WorkflowRoot: s.Layout.WorkflowRoot, GitMode: s.Layout.GitMode}); err != nil {
				t.Fatal(err)
			}
			kind, phase := "changes", "build"
			if engine == "to" {
				kind, phase = "tasks", "do"
			}
			statePath := filepath.Join(s.Layout.WorkflowRoot, kind, "native", engine+"-state.yaml")
			state := fmt.Sprintf("change: native\nid: generation-one\nphase: %s\nrepo_mode: explicit\nrepos: [api]\nrepo_bases:\n  api:\n    base_ref: %s\n    base_branch: main\n    git_common_dir: %s\n", phase, base, common)
			put(t, statePath, state)
			task, err := s.Create(context.Background(), "feature", engine, "native", "worktree", []string{"api"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			c := task.Checkouts["api"]
			if c.Owned || c.Base != base || c.BaseBranch != "main" || c.Path == api || runGit(t, c.Path, "rev-parse", "HEAD") != base {
				t.Fatalf("wrong native binding: %+v", c)
			}
			bindings, err := workspace.ListWorktrees(s.Layout)
			if err != nil || len(bindings) != 1 || bindings[0].Path != c.Path {
				t.Fatalf("native binding missing: %+v, %v", bindings, err)
			}
			if _, err := s.Load(task.Name); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RunCheck(context.Background(), task, "pair", nil); err != nil {
				t.Fatal(err)
			}
			put(t, statePath, strings.Replace(state, "generation-one", "generation-two", 1))
			if _, err := s.Load(task.Name); err == nil {
				t.Fatal("resumed another lifecycle generation")
			}
		})
	}
}

func TestFallbackAndMetadataIntegrity(t *testing.T) {
	s := fixture(t)
	s.Config.Integration = map[string]config.IntegrationCheck{}
	task := createNo(t, s, "existing")
	if !task.NeedsFallback {
		t.Fatal("missing fallback signal")
	}
	if _, err := s.RunCheck(context.Background(), task, "missing", nil); err == nil {
		t.Fatal("unconfigured check accepted")
	}
	c := task.Checkouts["api"]
	c.Path = s.Layout.Repos["web"]
	task.Checkouts["api"] = c
	if err := s.save(task); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(task.Name); err == nil {
		t.Fatal("rebound checkout accepted")
	}
}

func TestAllocationFailureRetainsExactIntent(t *testing.T) {
	s := fixture(t)
	api := s.Layout.Repos["api"]
	runGit(t, api, "branch", "work-feature-api")
	before := runGit(t, api, "rev-parse", "HEAD")
	task, err := s.Create(context.Background(), "feature", "no", "", "worktree", []string{"api"}, nil)
	if err == nil || task == nil {
		t.Fatalf("branch collision = %+v, %v", task, err)
	}
	dir, err := s.dir("feature")
	if err != nil {
		t.Fatal(err)
	}
	var recorded Task
	if err := readJSON(filepath.Join(dir, "task.json"), &recorded); err != nil {
		t.Fatal(err)
	}
	c := recorded.Checkouts["api"]
	if c.Ready || !c.Owned || c.Path != filepath.Join(s.Layout.WorktreesDir, "api", "workspace-feature") {
		t.Fatalf("lost pending allocation destination: %+v", c)
	}
	if _, err := s.Load("feature"); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("partial allocation resumed: %v", err)
	}
	if before != runGit(t, api, "rev-parse", "HEAD") || runGit(t, api, "status", "--porcelain") != "" {
		t.Fatal("failed allocation changed original")
	}
}

func TestDeclaredDottedAliasCanAllocate(t *testing.T) {
	s := fixture(t)
	data, err := os.ReadFile(s.Layout.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "'api'", "'api.v2'")
	text = strings.ReplaceAll(text, "api='../api'", "\"api.v2\"='../api'")
	put(t, s.Layout.ConfigPath, text)
	s, err = Open(s.Layout.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.Create(context.Background(), "feature", "no", "", "worktree", []string{"api.v2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(task.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunCheck(context.Background(), task, "pair", nil); err != nil {
		t.Fatal(err)
	}
}
