package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workflowroot"
)

func wtGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := worktreeGit(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wtWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func wtRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func wtRepo(t *testing.T, parent, name, branch string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wtGit(t, dir, "init", "-b", branch)
	wtGit(t, dir, "config", "user.name", "Worktree Test")
	wtGit(t, dir, "config", "user.email", "worktree@example.invalid")
	wtGit(t, dir, "config", "commit.gpgsign", "false")
	wtWrite(t, filepath.Join(dir, "tracked"), "base\n")
	wtGit(t, dir, "add", "tracked")
	wtGit(t, dir, "commit", "-m", "base")
	return dir
}

func wtFixture(t *testing.T, workflow string) Layout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	a := wtRepo(t, root, "source-a", "main")
	b := wtRepo(t, root, "source-b", "develop")
	wtWrite(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version = 2\n[workflow]\nroot = 'records'\n[worktrees]\ndir = 'execution'\n[repos]\na = %q\nb = %q\n", a, b))
	l, err := LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := json.Marshal(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: l.ConfigPath, WorkflowRoot: l.WorkflowRoot, GitMode: l.GitMode})
	if err != nil {
		t.Fatal(err)
	}
	wtWrite(t, filepath.Join(root, ".homonto", workflowroot.LayoutMarkerFile), string(marker))
	wtState(t, l, workflow, "feature", "generation-one", false, "a", "b")
	return l
}

func wtState(t *testing.T, l Layout, workflow, change, id string, terminal bool, repos ...string) {
	t.Helper()
	phase := "build"
	if workflow == "to" {
		phase = "do"
		if terminal {
			phase = "done"
		}
	}
	text := fmt.Sprintf("change: %s\nid: %q\ncreated: 2026-09-07\nphase: %s\narchived: %t\nrepos:\n", change, id, phase, terminal)
	for _, repo := range repos {
		text += fmt.Sprintf("  - %q\n", repo)
	}
	wtWrite(t, filepath.Join(worktreeStatesDir(l, workflow), change, workflow+"-state.yaml"), text)
}

func wtCreate(t *testing.T, l Layout, workflow, repo, base string) Worktree {
	t.Helper()
	w, err := CreateWorktree(l, workflow, "feature", repo, base, "work/feature-"+repo)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func wtError(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error = %v, want containing %q", err, fragment)
	}
}

func TestWorktreeMultiRepoDirtyOriginalsUnchanged(t *testing.T) {
	l := wtFixture(t, "onto")
	type snapshot struct{ index, file, head, branch, status string }
	before := map[string]snapshot{}
	for alias, dir := range l.Repos {
		wtWrite(t, filepath.Join(dir, "tracked"), "staged original\n")
		wtGit(t, dir, "add", "tracked")
		wtWrite(t, filepath.Join(dir, "tracked"), "unstaged original\n")
		wtWrite(t, filepath.Join(dir, "untracked"), "original untracked\n")
		before[alias] = snapshot{string(wtRead(t, filepath.Join(dir, ".git", "index"))), string(wtRead(t, filepath.Join(dir, "tracked"))),
			wtGit(t, dir, "rev-parse", "HEAD"), wtGit(t, dir, "symbolic-ref", "HEAD"), wtGit(t, dir, "status", "--porcelain=v1", "-uall")}
	}
	a := wtCreate(t, l, "onto", "a", "main")
	b := wtCreate(t, l, "onto", "b", "develop")
	if a.BaseTarget != "refs/heads/main" || b.BaseTarget != "refs/heads/develop" {
		t.Fatalf("wrong per-repo integration targets: %+v / %+v", a, b)
	}
	if a.BaseCommit != before["a"].head || b.BaseCommit != before["b"].head {
		t.Fatal("base refs were not frozen to commits")
	}
	for alias, dir := range l.Repos {
		got := snapshot{string(wtRead(t, filepath.Join(dir, ".git", "index"))), string(wtRead(t, filepath.Join(dir, "tracked"))),
			wtGit(t, dir, "rev-parse", "HEAD"), wtGit(t, dir, "symbolic-ref", "HEAD"), wtGit(t, dir, "status", "--porcelain=v1", "-uall")}
		if got != before[alias] {
			t.Fatalf("original %s changed: before %+v, after %+v", alias, before[alias], got)
		}
	}
	dirs, err := SourceDirs(l.ConfigRoot, "onto", "feature", []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dirs, map[string]string{"a": a.Path, "b": b.Path}) {
		t.Fatalf("execution dirs = %#v", dirs)
	}
	if string(wtRead(t, filepath.Join(a.Path, "tracked"))) != "base\n" {
		t.Fatal("execution checkout contains original dirt")
	}
	if _, err := os.Stat(filepath.Join(l.ConfigRoot, ".git")); !os.IsNotExist(err) {
		t.Fatal("config parent must remain non-Git")
	}
	config := wtRead(t, l.ConfigPath)
	again := wtCreate(t, l, "onto", "a", "main")
	if again != a || !bytes.Equal(config, wtRead(t, l.ConfigPath)) {
		t.Fatal("create was not idempotent or retargeted config")
	}
	listed, err := ListWorktrees(l)
	if err != nil || len(listed) != 2 || listed[0].Repo != "a" || listed[1].Repo != "b" {
		t.Fatalf("list = %+v, %v", listed, err)
	}
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "different")
	wtError(t, err, "different base or branch")
}

func TestWorktreeSourceDirsLegacyAndSelection(t *testing.T) {
	root := t.TempDir()
	dirs, err := SourceDirs(root, "onto", "feature", nil)
	if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": root}) {
		t.Fatalf("absent legacy config = %v, %v", dirs, err)
	}
	_, err = SourceDirs(root, "onto", "feature", []string{"missing"})
	wtError(t, err, "missing declaration")
	wtWrite(t, filepath.Join(root, "homonto.toml"), "broken = [")
	_, err = SourceDirs(root, "onto", "feature", nil)
	wtError(t, err, "parse config")
	repo := wtRepo(t, root, "source", "main")
	wtWrite(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("[repos]\n_source = %q\n", repo))
	dirs, err = SourceDirs(root, "to", "feature", []string{"_source"})
	if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": root, "_source": repo}) {
		t.Fatalf("legacy aliases = %v, %v", dirs, err)
	}
	l := wtFixture(t, "onto")
	dirs, err = SourceDirs(l.ConfigRoot, "onto", "feature", nil)
	if err != nil || len(dirs) != 0 {
		t.Fatalf("v2 must not implicitly select config or aliases: %v, %v", dirs, err)
	}
	dirs, err = SourceDirs(l.ConfigRoot, "onto", "feature", []string{"b"})
	if err != nil || !reflect.DeepEqual(dirs, map[string]string{"b": l.Repos["b"]}) {
		t.Fatalf("unbound declared checkout = %v, %v", dirs, err)
	}
	_, err = SourceDirs(l.ConfigRoot, "onto", "feature", []string{"z", "c"})
	wtError(t, err, `repository "c"`)
	_, err = SourceDirs(l.ConfigRoot, "onto", "feature", []string{"a", "a"})
	wtError(t, err, "duplicate")
	wtWrite(t, l.ConfigPath, "schema_version = 2\n[repos]\na = 'missing-repo'\n")
	_, err = SourceDirs(l.ConfigRoot, "onto", "feature", nil)
	wtError(t, err, "does not exist")
}

func TestWorktreeRejectsWrongBindings(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		alter      func(*testing.T, Layout, *Worktree)
	}{
		{"common-dir", "common-dir mismatch", func(t *testing.T, l Layout, w *Worktree) { w.CommonDir = filepath.Join(l.Repos["b"], ".git") }},
		{"path", "unsafe path", func(t *testing.T, l Layout, w *Worktree) { w.Path = l.Repos["b"] }},
		{"branch", "expected attached branch", func(t *testing.T, l Layout, w *Worktree) { wtGit(t, w.Path, "checkout", "-b", "wrong") }},
		{"detached", "expected attached branch", func(t *testing.T, l Layout, w *Worktree) { wtGit(t, w.Path, "checkout", "--detach") }},
		{"gitdir", "identity changed", func(t *testing.T, l Layout, w *Worktree) { w.GitDir += "-wrong" }},
		{"replacement", "creation token mismatch", func(t *testing.T, l Layout, w *Worktree) {
			wtGit(t, l.Repos["a"], "worktree", "remove", w.Path)
			wtGit(t, l.Repos["a"], "worktree", "add", w.Path, w.Branch)
		}},
		{"foreign-worktree", "common-dir mismatch", func(t *testing.T, l Layout, w *Worktree) {
			wtGit(t, l.Repos["a"], "worktree", "remove", w.Path)
			wtGit(t, l.Repos["b"], "worktree", "add", "-b", w.Branch, w.Path, "develop")
		}},
		{"stale", "registered path unavailable", func(t *testing.T, l Layout, w *Worktree) { wtGit(t, l.Repos["a"], "worktree", "remove", w.Path) }},
		{"pending", "incomplete registry", func(t *testing.T, l Layout, w *Worktree) { w.Status = "creating" }},
		{"generation", "state identity mismatch", func(t *testing.T, l Layout, w *Worktree) {
			wtState(t, l, "onto", "feature", "generation-two", false, "a", "b")
		}},
		{"parent-change", "parent changes do not retarget", func(t *testing.T, l Layout, w *Worktree) {
			wtWrite(t, l.ConfigPath, strings.ReplaceAll(string(wtRead(t, l.ConfigPath)), "execution", "other-execution"))
		}},
		{"authority-change", "common-dir mismatch", func(t *testing.T, l Layout, w *Worktree) {
			other := wtRepo(t, l.ConfigRoot, "other-source", "main")
			wtWrite(t, l.ConfigPath, strings.ReplaceAll(string(wtRead(t, l.ConfigPath)), l.Repos["a"], other))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := wtFixture(t, "onto")
			w := wtCreate(t, l, "onto", "a", "main")
			tc.alter(t, l, &w)
			if err := saveWorktreeRegistry(l, worktreeRegistry{Version: 1, Entries: []Worktree{w}}); err != nil {
				t.Fatal(err)
			}
			_, err := SourceDirs(l.ConfigRoot, "onto", "feature", []string{"a"})
			wtError(t, err, tc.want)
		})
	}
}

func TestWorktreeNamesAndPreconditions(t *testing.T) {
	l := wtFixture(t, "onto")
	for _, name := range []string{"../escape", "/absolute", `a\b`, "a/b", ".", "..", "archive", "-option", "a\nb"} {
		_, err := CreateWorktree(l, "onto", name, "a", "main", "work/test")
		wtError(t, err, "unsafe path")
	}
	_, err := CreateWorktree(l, "onto", "feature", "../a", "main", "work/test")
	wtError(t, err, "unsafe path")
	_, err = CreateWorktree(l, "other", "feature", "a", "main", "work/test")
	wtError(t, err, "workflow must")
	missing := l
	missing.WorktreesDir = ""
	_, err = CreateWorktree(missing, "onto", "feature", "a", "main", "work/test")
	wtError(t, err, "missing declaration")
	_, err = CreateWorktree(l, "onto", "missing", "a", "main", "work/test")
	wtError(t, err, "matching active state")
	_, err = CreateWorktree(l, "onto", "feature", "c", "main", "work/test")
	wtError(t, err, "missing declaration")
	wtState(t, l, "onto", "feature", "generation-one", false, "b")
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "work/test")
	wtError(t, err, "not selected")
	wtState(t, l, "onto", "feature", "generation-one", false, "a", "b")
	_, err = CreateWorktree(l, "onto", "feature", "a", "", "work/test")
	wtError(t, err, "explicit --base")
	_, err = CreateWorktree(l, "onto", "feature", "a", "missing-ref", "work/test")
	wtError(t, err, "rev-parse")
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "main")
	wtError(t, err, "already exists")
	dest := filepath.Join(l.WorktreesDir, "a", "onto-feature")
	wtWrite(t, filepath.Join(dest, "unknown"), "must survive")
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "work/test")
	wtError(t, err, "refusing overwrite")
	if string(wtRead(t, filepath.Join(dest, "unknown"))) != "must survive" {
		t.Fatal("unknown path was modified")
	}
	if _, err := os.Stat(registryPath(l)); !os.IsNotExist(err) {
		t.Fatal("failed preconditions must not create registry")
	}
}

func TestWorktreeTerminalRemovalSafety(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			l := wtFixture(t, workflow)
			w := wtCreate(t, l, workflow, "a", "HEAD")
			if w.BaseTarget != "refs/heads/main" {
				t.Fatalf("HEAD was not anchored to authority branch: %+v", w)
			}
			_, err := RemoveWorktree(l, workflow, "feature", "a", false)
			wtError(t, err, "requires --yes")
			_, err = RemoveWorktree(l, workflow, "feature", "a", true)
			wtError(t, err, "active binding")
			wtWrite(t, filepath.Join(w.Path, "tracked"), "source change\n")
			wtGit(t, w.Path, "add", "tracked")
			wtGit(t, w.Path, "commit", "-m", "source change")
			wtState(t, l, workflow, "feature", "generation-one", true, "a", "b")
			_, err = RemoveWorktree(l, workflow, "feature", "a", true)
			wtError(t, err, "unmerged source commit")
			wtGit(t, l.Repos["a"], "merge", "--ff-only", w.Branch)
			for _, name := range []string{"untracked", "ignored"} {
				if name == "ignored" {
					wtWrite(t, filepath.Join(w.CommonDir, "info", "exclude"), "ignored\n")
				}
				wtWrite(t, filepath.Join(w.Path, name), "preserve me\n")
				_, err = RemoveWorktree(l, workflow, "feature", "a", true)
				wtError(t, err, "dirty registered path")
				if err := os.Remove(filepath.Join(w.Path, name)); err != nil {
					t.Fatal(err)
				}
			}
			wtWrite(t, filepath.Join(l.Repos["a"], "tracked"), "original stays dirty\n")
			originalIndex := wtRead(t, filepath.Join(l.Repos["a"], ".git", "index"))
			removed, err := RemoveWorktree(l, workflow, "feature", "a", true)
			if err != nil || removed.Path != w.Path {
				t.Fatalf("remove = %+v, %v", removed, err)
			}
			if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
				t.Fatalf("worktree still exists: %v", err)
			}
			if !bytes.Equal(originalIndex, wtRead(t, filepath.Join(l.Repos["a"], ".git", "index"))) || string(wtRead(t, filepath.Join(l.Repos["a"], "tracked"))) != "original stays dirty\n" {
				t.Fatal("removal changed original index or files")
			}
			wtGit(t, l.Repos["a"], "show-ref", "--verify", "refs/heads/"+w.Branch)
			entries, err := ListWorktrees(l)
			if err != nil || len(entries) != 0 {
				t.Fatalf("registry after removal = %+v, %v", entries, err)
			}
		})
	}
}

func TestWorktreeArchiveIdentityAndLegacyGeneration(t *testing.T) {
	for _, id := range []string{"stable-id", ""} {
		t.Run("id="+id, func(t *testing.T) {
			l := wtFixture(t, "to")
			wtState(t, l, "to", "feature", id, false, "a")
			w := wtCreate(t, l, "to", "a", "main")
			wtState(t, l, "to", "feature", id, true, "a")
			active := filepath.Join(worktreeStatesDir(l, "to"), "feature")
			archive := filepath.Join(worktreeStatesDir(l, "to"), "archive", "2026-09-07-feature")
			if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(active, archive); err != nil {
				t.Fatal(err)
			}
			dirs, err := SourceDirs(l.ConfigRoot, "to", "feature", []string{"a"})
			if err != nil || dirs["a"] != w.Path {
				t.Fatalf("archive binding = %v, %v", dirs, err)
			}
			// Same name and Created date, but a new directory (and ID if present).
			newID := ""
			if id != "" {
				newID = "different-id"
			}
			wtState(t, l, "to", "feature", newID, false, "a")
			_, err = SourceDirs(l.ConfigRoot, "to", "feature", []string{"a"})
			wtError(t, err, "state identity mismatch")
			_, err = CreateWorktree(l, "to", "feature", "a", "main", w.Branch)
			wtError(t, err, "state identity mismatch")
			_, err = RemoveWorktree(l, "to", "feature", "a", true)
			wtError(t, err, "state identity mismatch")
			if err := os.RemoveAll(active); err != nil {
				t.Fatal(err)
			}
			if _, err := RemoveWorktree(l, "to", "feature", "a", true); err != nil {
				t.Fatalf("integrated terminal archive removal: %v", err)
			}
		})
	}
}

func TestWorktreeSymlinksAndRegistryLock(t *testing.T) {
	for _, target := range []string{"parent", "repo-dir", "registry", "state"} {
		t.Run(target, func(t *testing.T) {
			l := wtFixture(t, "onto")
			outside := t.TempDir()
			var link string
			switch target {
			case "parent":
				link = l.WorktreesDir
			case "repo-dir":
				link = filepath.Join(l.WorktreesDir, "a")
			case "registry":
				link = filepath.Join(l.ConfigRoot, ".homonto")
				if err := os.RemoveAll(link); err != nil {
					t.Fatal(err)
				}
			case "state":
				link = filepath.Join(worktreeStatesDir(l, "onto"), "feature", "onto-state.yaml")
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			_, err := CreateWorktree(l, "onto", "feature", "a", "main", "work/feature")
			wtError(t, err, "unsafe path")
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("symlink target was changed: %v, %v", entries, err)
			}
		})
	}
	l := wtFixture(t, "onto")
	unlock, err := lockWorktreeRegistry(l)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "work/feature")
	wtError(t, err, "registry lock")
	unlock()
	wtCreate(t, l, "onto", "a", "main")
	for _, text := range []string{`{"version":1,"entries":[]} trailing`, `{}`, `null`, `{"version":1}`, `{"version":1,"entries":null}`} {
		wtWrite(t, registryPath(l), text)
		_, err = SourceDirs(l.ConfigRoot, "onto", "feature", []string{"a"})
		wtError(t, err, "invalid registry")
	}
}

func TestWorktreeInterruptedCreateRetainsIntent(t *testing.T) {
	l := wtFixture(t, "onto")
	// Git refuses to overwrite an administrative worktree reservation even if
	// its destination is gone. This fails after the registry intent is saved.
	stale := filepath.Join(l.Repos["a"], ".git", "worktrees", "onto-feature")
	wtWrite(t, filepath.Join(stale, "gitdir"), filepath.Join(l.WorktreesDir, "a", "onto-feature", ".git")+"\n")
	wtWrite(t, filepath.Join(stale, "HEAD"), "ref: refs/heads/unknown\n")
	wtWrite(t, filepath.Join(stale, "commondir"), "../..\n")
	wtWrite(t, filepath.Join(stale, "locked"), "unknown owner\n")
	_, err := CreateWorktree(l, "onto", "feature", "a", "main", "work/feature-a")
	wtError(t, err, "pending registry retained")
	r, err := readWorktreeRegistry(l)
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Status != "creating" {
		t.Fatalf("interrupted intent = %+v, %v", r, err)
	}
	wtWrite(t, filepath.Join(r.Entries[0].Path, "unknown"), "preserve after failure\n")
	_, err = CreateWorktree(l, "onto", "feature", "a", "main", "work/feature-a")
	wtError(t, err, "incomplete registry")
	_, err = RemoveWorktree(l, "onto", "feature", "a", true)
	wtError(t, err, "incomplete registry")
	if string(wtRead(t, filepath.Join(r.Entries[0].Path, "unknown"))) != "preserve after failure\n" {
		t.Fatal("recovery deleted unknown work")
	}
}

func TestWorktreeLockedRemovalLeavesReadyRegistry(t *testing.T) {
	l := wtFixture(t, "to")
	w := wtCreate(t, l, "to", "a", "main")
	wtState(t, l, "to", "feature", "generation-one", true, "a", "b")
	wtGit(t, l.Repos["a"], "worktree", "lock", w.Path)
	_, err := RemoveWorktree(l, "to", "feature", "a", true)
	wtError(t, err, "locked")
	r, err := readWorktreeRegistry(l)
	if err != nil || len(r.Entries) != 1 || r.Entries[0].Status != "ready" {
		t.Fatalf("failed remove must retain ready binding: %+v, %v", r, err)
	}
	wtGit(t, l.Repos["a"], "worktree", "unlock", w.Path)
	if _, err := RemoveWorktree(l, "to", "feature", "a", true); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeBaseCommitStaysImmutable(t *testing.T) {
	l := wtFixture(t, "to")
	w := wtCreate(t, l, "to", "a", "main")
	wtWrite(t, filepath.Join(l.Repos["a"], "tracked"), "base advanced\n")
	wtGit(t, l.Repos["a"], "commit", "-am", "advance base")
	again := wtCreate(t, l, "to", "a", "main")
	if again != w || wtGit(t, w.Path, "rev-parse", "HEAD") != w.BaseCommit {
		t.Fatal("idempotent create moved the immutable execution base")
	}
}

func TestWorktreeRemovalRefusesHiddenIndexChanges(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree", "both", "sparse"} {
		t.Run(flag, func(t *testing.T) {
			l := wtFixture(t, "to")
			name := "hidden\ntracked"
			wtWrite(t, filepath.Join(l.Repos["a"], name), "committed\n")
			wtGit(t, l.Repos["a"], "add", "--", name)
			wtGit(t, l.Repos["a"], "commit", "-m", "extra tracked file")
			w := wtCreate(t, l, "to", "a", "main")
			wtState(t, l, "to", "feature", "generation-one", true, "a", "b")
			switch flag {
			case "sparse":
				wtGit(t, w.Path, "sparse-checkout", "set", "--no-cone", "/tracked")
			case "both":
				wtGit(t, w.Path, "update-index", "--assume-unchanged", "--", name)
				wtGit(t, w.Path, "update-index", "--skip-worktree", "--", name)
			default:
				wtGit(t, w.Path, "update-index", flag, "--", name)
			}
			if flag != "sparse" {
				wtWrite(t, filepath.Join(w.Path, name), "hidden modification must survive\n")
			}
			if dirt := wtGit(t, w.Path, "status", "--porcelain=v1"); dirt != "" {
				t.Fatalf("fixture must hide changes from git status: %q", dirt)
			}
			index := wtRead(t, filepath.Join(w.GitDir, "index"))
			registry := wtRead(t, registryPath(l))
			_, err := RemoveWorktree(l, "to", "feature", "a", true)
			wtError(t, err, "nondefault index flags")
			if !bytes.Equal(index, wtRead(t, filepath.Join(w.GitDir, "index"))) || !bytes.Equal(registry, wtRead(t, registryPath(l))) {
				t.Fatal("blocked removal changed index or registry")
			}
			if flag != "sparse" && string(wtRead(t, filepath.Join(w.Path, name))) != "hidden modification must survive\n" {
				t.Fatal("blocked removal changed hidden tracked contents")
			}
			if flag == "sparse" {
				if _, err := os.Lstat(filepath.Join(w.Path, name)); !os.IsNotExist(err) {
					t.Fatal("blocked removal materialized a sparse file")
				}
			}
		})
	}
}

func TestWorktreeCreateMatchesRecordedRepoBases(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		for _, scenario := range []string{"diverged", "same-commit-wrong-target", "advanced-base", "matching-alternative"} {
			t.Run(workflow+"/"+scenario, func(t *testing.T) {
				l := wtFixture(t, workflow)
				dir := l.Repos["a"]
				wtGit(t, dir, "checkout", "-b", "feature-base")
				if scenario != "same-commit-wrong-target" {
					wtWrite(t, filepath.Join(dir, "tracked"), "feature base\n")
					wtGit(t, dir, "commit", "-am", "feature base")
				}
				commit := wtGit(t, dir, "rev-parse", "HEAD")
				if scenario == "advanced-base" {
					wtWrite(t, filepath.Join(dir, "tracked"), "advanced base\n")
					wtGit(t, dir, "commit", "-am", "advance recorded base")
				}
				statePath := filepath.Join(worktreeStatesDir(l, workflow), "feature", workflow+"-state.yaml")
				state := string(wtRead(t, statePath)) + fmt.Sprintf("repo_bases:\n  a:\n    base_ref: %q\n    base_branch: feature-base\n    git_common_dir: %q\n", commit, filepath.Join(dir, ".git"))
				wtWrite(t, statePath, state)
				beforeRefs := wtGit(t, dir, "show-ref")
				beforeIndex := wtRead(t, filepath.Join(dir, ".git", "index"))
				beforeControl, err := os.Stat(filepath.Join(l.ConfigRoot, ".homonto"))
				if err != nil {
					t.Fatal(err)
				}
				base := "main"
				if scenario == "advanced-base" || scenario == "matching-alternative" {
					base = "feature-base"
				}
				w, err := CreateWorktree(l, workflow, "feature", "a", base, "work/feature-a")
				if scenario == "matching-alternative" {
					if err != nil || w.BaseCommit != commit || w.BaseTarget != "refs/heads/feature-base" {
						t.Fatalf("matching alternative base = %+v, %v", w, err)
					}
					dirs, err := SourceDirs(l.ConfigRoot, workflow, "feature", []string{"a"})
					if err != nil || dirs["a"] != w.Path {
						t.Fatalf("ready binding = %v, %v", dirs, err)
					}
					// Idempotency uses the recorded immutable base, not its later tip.
					wtWrite(t, filepath.Join(dir, "tracked"), "later base tip\n")
					wtGit(t, dir, "commit", "-am", "later base tip")
					if again := wtCreate(t, l, workflow, "a", base); again != w {
						t.Fatal("idempotent binding changed with the base branch")
					}
					return
				}
				wtError(t, err, "base mismatch")
				for _, path := range []string{registryPath(l), l.WorktreesDir, filepath.Join(l.ConfigRoot, ".homonto", "worktrees.lock")} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("base mismatch created %s: %v", path, err)
					}
				}
				afterControl, err := os.Stat(filepath.Join(l.ConfigRoot, ".homonto"))
				if err != nil {
					t.Fatal(err)
				}
				if !beforeControl.ModTime().Equal(afterControl.ModTime()) || beforeRefs != wtGit(t, dir, "show-ref") || !bytes.Equal(beforeIndex, wtRead(t, filepath.Join(dir, ".git", "index"))) || string(wtRead(t, statePath)) != state {
					t.Fatal("base mismatch wrote control plane, source refs/index, or state")
				}
			})
		}
	}
}

func TestWorktreeEnsureNameAvailablePreservesArchive(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			l := wtFixture(t, workflow)
			if err := EnsureNameAvailable(l, workflow, "feature"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(registryPath(l)); !os.IsNotExist(err) {
				t.Fatal("name lookup created a registry")
			}
			w := wtCreate(t, l, workflow, "a", "main")
			wtState(t, l, workflow, "feature", "generation-one", true, "a", "b")
			active := filepath.Join(worktreeStatesDir(l, workflow), "feature")
			archive := filepath.Join(worktreeStatesDir(l, workflow), "archive", "2026-09-07-feature")
			if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(active, archive); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(archive, workflow+"-state.yaml")
			state, registry, index := wtRead(t, statePath), wtRead(t, registryPath(l)), wtRead(t, filepath.Join(w.GitDir, "index"))
			wtError(t, EnsureNameAvailable(l, workflow, "feature"), "before reusing the change name")
			otherWorkflow := "onto"
			if workflow == "onto" {
				otherWorkflow = "to"
			}
			for _, key := range [][2]string{{otherWorkflow, "feature"}, {workflow, "other"}} {
				if err := EnsureNameAvailable(l, key[0], key[1]); err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(state, wtRead(t, statePath)) || !bytes.Equal(registry, wtRead(t, registryPath(l))) || !bytes.Equal(index, wtRead(t, filepath.Join(w.GitDir, "index"))) {
				t.Fatal("name guard modified old generation")
			}
			if _, err := os.Stat(active); !os.IsNotExist(err) {
				t.Fatal("name guard recreated active state")
			}
			// The blocked reuse leaves the archived generation removable.
			if _, err := RemoveWorktree(l, workflow, "feature", "a", true); err != nil {
				t.Fatal(err)
			}
			if err := EnsureNameAvailable(l, workflow, "feature"); err != nil {
				t.Fatalf("name should be available after cleanup: %v", err)
			}
			w.Status = "creating"
			if err := saveWorktreeRegistry(l, worktreeRegistry{Version: 1, Entries: []Worktree{w}}); err != nil {
				t.Fatal(err)
			}
			wtError(t, EnsureNameAvailable(l, workflow, "feature"), "registered binding")
			wtWrite(t, registryPath(l), "{}")
			wtError(t, EnsureNameAvailable(l, workflow, "feature"), "invalid registry")
		})
	}
}

func TestWorktreeLegacyEmptyScopeSkipsUnavailableDeclarations(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := t.TempDir()
			config := fmt.Sprintf("schema_version = %d\n[workflow]\nroot = 'records'\n[repos]\nunavailable = '../missing-repo'\n", version)
			wtWrite(t, filepath.Join(root, "homonto.toml"), config)
			l, err := LoadScopeRoot(root, nil)
			if err != nil || l.ConfigRoot != root || l.WorkflowRoot != filepath.Join(root, "records") || l.Repos != nil || l.SchemaVersion != version {
				t.Fatalf("legacy empty scope layout = %+v, %v", l, err)
			}
			dirs, err := SourceDirs(root, "onto", "feature", nil)
			if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": root}) {
				t.Fatalf("legacy empty source scope = %v, %v", dirs, err)
			}
			_, err = LoadScopeRoot(root, []string{"unavailable"})
			wtError(t, err, "does not exist")
			if _, err := os.Stat(filepath.Join(root, ".homonto")); !os.IsNotExist(err) {
				t.Fatal("scope loader wrote control plane")
			}
			// The shortcut must still inspect bindings, not simply return root.
			w := Worktree{Workflow: "onto", Change: "feature", Repo: "", StateID: "id:old", Path: filepath.Join(root, "old-execution")}
			if err := saveWorktreeRegistry(l, worktreeRegistry{Version: 1, Entries: []Worktree{w}}); err != nil {
				t.Fatal(err)
			}
			_, err = SourceDirs(root, "onto", "feature", nil)
			wtError(t, err, "missing declaration")
			wtWrite(t, registryPath(l), "{}")
			_, err = SourceDirs(root, "onto", "feature", nil)
			wtError(t, err, "invalid registry")
		})
	}
	for _, config := range []string{
		"schema_version = 99\n", "schema_version = -1\n", "schema_version = 'wrong'\n", "broken = [",
		"[repos]\nbroken = 42\n", "[workflow]\nroot = '../escape'\n", "[workflow]\nroot = 'records/..'\n",
		"[workflow]\nroot = '/absolute'\n", "[worktrees]\ndir = 'execution'\n",
		"[workflow]\ngit = 'managed'\n", "schema_version = 2\n[repos]\nunavailable = '../missing-repo'\n",
	} {
		t.Run(config, func(t *testing.T) {
			root := t.TempDir()
			wtWrite(t, filepath.Join(root, "homonto.toml"), config)
			if _, err := LoadScopeRoot(root, nil); err == nil {
				t.Fatalf("empty scope accepted unsafe or malformed config %q", config)
			}
		})
	}
}
