package tocli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestNewExplicitCapturesImmutableAnchors(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-branches", true: "selected-bases"}[override], func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			branches := map[string]string{"api": "main", "web": "develop"}
			want := map[string]tostate.RepoBase{}
			originals := map[string][]byte{}
			args := []string{"new", "anchored", "--repo", "api,web", "--dir", l.ConfigRoot}
			for alias, branch := range branches {
				dir := l.Repos[alias]
				git(t, dir, "branch", "-M", branch)
				head, err := repoGitOutput(dir, "rev-parse", "HEAD")
				if err != nil {
					t.Fatal(err)
				}
				want[alias] = tostate.RepoBase{GitCommonDir: filepath.Join(dir, ".git"), BaseRef: strings.TrimSpace(string(head)), BaseBranch: branch}
				if override {
					git(t, dir, "checkout", "-b", "feature")
					writeFile(t, filepath.Join(dir, "feature"), "existing feature work\n")
					git(t, dir, "add", "feature")
					git(t, dir, "commit", "-m", "feature before new")
					args = append(args, "--base", alias+"="+branch)
				}
				writeFile(t, filepath.Join(dir, "tracked"), "staged original work\n")
				git(t, dir, "add", "tracked")
				writeFile(t, filepath.Join(dir, "tracked"), "unstaged original work\n")
				writeFile(t, filepath.Join(dir, "untracked"), "original untracked work\n")
				for _, file := range []string{".git/HEAD", ".git/index", "tracked", "untracked"} {
					originals[alias+file], err = os.ReadFile(filepath.Join(dir, file))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			run(t, false, args...)
			st, err := tostate.Load(statePath(l.ConfigRoot, "anchored"))
			if err != nil || !reflect.DeepEqual(st.RepoBases, want) {
				t.Fatalf("new anchors = %+v, %v; want %+v", st.RepoBases, err, want)
			}
			for alias, dir := range l.Repos {
				for _, file := range []string{".git/HEAD", ".git/index", "tracked", "untracked"} {
					data, err := os.ReadFile(filepath.Join(dir, file))
					if err != nil || string(data) != string(originals[alias+file]) {
						t.Fatalf("new changed original %s/%s: %v", alias, file, err)
					}
				}
				git(t, dir, "checkout", "-b", "implementation")
				git(t, dir, "add", "-A")
				git(t, dir, "commit", "-m", "implementation after new")
			}
			run(t, false, "phase", "anchored", "--dir", l.ConfigRoot)
			st, err = tostate.Load(statePath(l.ConfigRoot, "anchored"))
			if err != nil || !reflect.DeepEqual(st.RepoBases, want) {
				t.Fatalf("implementation changed recorded anchors = %+v, %v", st.RepoBases, err)
			}
		})
	}
}

func TestNewExplicitRejectsInvalidBasesBeforeWrites(t *testing.T) {
	for _, bases := range [][]string{
		{"api"}, {"=main"}, {"api="}, {"missing=main"}, {"web=main"},
		{"api=main", "api=main"}, {"api=absent"}, {"api=v1"}, {"api=tag-only"},
		{"api=HEAD"}, {"api=main~1"}, {"api=main^{commit}"}, {"api=-bad"},
		{"api=bad branch"}, {"api=refs/remotes/origin/main"},
	} {
		t.Run(strings.Join(bases, ","), func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			git(t, l.Repos["api"], "branch", "-M", "main")
			git(t, l.Repos["api"], "tag", "v1")
			git(t, l.Repos["api"], "tag", "refs/heads/tag-only")
			git(t, l.Repos["api"], "update-ref", "refs/remotes/origin/main", "HEAD")
			args := []string{"new", "invalid", "--repo", "api", "--dir", l.ConfigRoot}
			for _, base := range bases {
				args = append(args, "--base", base)
			}
			runErr(t, args...)
			if _, err := os.Stat(filepath.Join(l.WorkflowRoot, "tasks")); !os.IsNotExist(err) {
				t.Fatalf("invalid base created workflow state: %v", err)
			}
		})
	}
}

func TestNewDetachedSourceRequiresExplicitLocalBase(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	dir := l.Repos["api"]
	git(t, dir, "branch", "-M", "main")
	git(t, dir, "checkout", "--detach")
	out := runErr(t, "new", "detached", "--repo", "api", "--dir", l.ConfigRoot)
	if !strings.Contains(out, "--base api=branch") {
		t.Fatalf("detached refusal lacks recovery: %s", out)
	}
	run(t, false, "new", "detached", "--repo", "api", "--base", "api=refs/heads/main", "--dir", l.ConfigRoot)
	st, err := tostate.Load(statePath(l.ConfigRoot, "detached"))
	if err != nil || st.RepoBases["api"].BaseBranch != "main" {
		t.Fatalf("explicit detached base = %+v, %v", st.RepoBases, err)
	}
	if _, err := repoGitOutput(dir, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		t.Fatal("new changed detached HEAD")
	}
}

func TestNewLegacyUnscopedDoesNotResolveUnusedRepos(t *testing.T) {
	root := setUpGatedWorkspace(t)
	config := filepath.Join(root, "homonto.toml")
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, config, string(data)+"\n[repos]\noffline = '../missing-repository'\n")
	run(t, false, "new", "unscoped", "--dir", root)
	st, err := tostate.Load(statePath(root, "unscoped"))
	if err != nil || st.RepoMode != tostate.SourceLegacy || len(st.RepoBases) != 0 {
		t.Fatalf("legacy unscoped = %+v, %v", st, err)
	}
	runErr(t, "new", "scoped", "--repo", "offline", "--dir", root)
	runErr(t, "new", "base", "--base", "offline=main", "--dir", root)
}

func TestNewRefusesArchivedNameWithRetainedBinding(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	run(t, false, "new", "recurring", "--repo", "api", "--dir", l.ConfigRoot)
	w, err := workspace.CreateWorktree(l, "to", "recurring", "api", "HEAD", "recurring-work")
	if err != nil {
		t.Fatal(err)
	}
	run(t, false, "phase", "recurring", "--dir", l.ConfigRoot)
	run(t, false, "done", "recurring", "--verified", "--dir", l.ConfigRoot)
	archive := filepath.Join(findArchived(l.ConfigRoot, "recurring"), tostate.FileName)
	before, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	// Selecting a different source must not orphan a retained binding either.
	out := runErr(t, "new", "recurring", "--repo", "web", "--dir", l.ConfigRoot)
	if !strings.Contains(out, "registered binding") || !strings.Contains(out, w.Path) {
		t.Fatalf("retained binding refusal = %s", out)
	}
	if _, err := os.Stat(changeDir(l.ConfigRoot, "recurring")); !os.IsNotExist(err) {
		t.Fatalf("refused name created new state: %v", err)
	}
	after, err := os.ReadFile(archive)
	if err != nil || string(before) != string(after) {
		t.Fatalf("refused name modified archive: %v", err)
	}
	if _, err := workspace.RemoveWorktree(l, "to", "recurring", "api", true); err != nil {
		t.Fatal(err)
	}
	run(t, false, "new", "recurring", "--repo", "web", "--dir", l.ConfigRoot)
}
