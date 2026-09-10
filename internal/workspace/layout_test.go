package workspace_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestLayoutNonGitRootTwoRepos(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "control plane")
	a, b := filepath.Join(base, "service a"), filepath.Join(base, "service b")
	git(t, "init", "-q", a)
	git(t, "init", "-q", b)
	path := filepath.Join(root, "homonto.toml")
	write(t, path, "schema_version=2\n[repos]\na='../service a'\nb='../service b'\n[workflow]\ngit='managed'\nroot='../records home'\n[worktrees]\ndir='../allocated worktrees'\n")
	l, err := workspace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := workspace.Layout{SchemaVersion: 2, ConfigPath: path, ConfigRoot: root, WorkflowRoot: filepath.Join(base, "records home"), GitMode: "managed", WorktreesDir: filepath.Join(base, "allocated worktrees"), Repos: map[string]string{"a": a, "b": b}}
	if !reflect.DeepEqual(l, want) || !l.ExplicitRepos() {
		t.Fatalf("layout = %#v, want %#v", l, want)
	}
	fromRoot, err := workspace.LoadRoot(root)
	if err != nil || !reflect.DeepEqual(l, fromRoot) {
		t.Fatalf("LoadRoot = %#v, %v", fromRoot, err)
	}
	for _, p := range []string{l.WorkflowRoot, l.WorktreesDir, filepath.Join(root, ".git"), filepath.Join(root, ".homonto")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("Load wrote %q: %v", p, err)
		}
	}
}

func TestLayoutDefaultsAndLegacy(t *testing.T) {
	for _, version := range []string{"", "schema_version=0\n", "schema_version=1\n", "schema_version=2\n"} {
		t.Run(version, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "homonto.toml")
			write(t, path, version)
			l, err := workspace.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if l.WorkflowRoot != filepath.Join(root, "docs") || l.GitMode != "existing" || l.WorktreesDir != "" || len(l.Repos) != 0 || l.ExplicitRepos() != strings.Contains(version, "=2") {
				t.Fatalf("unexpected defaults: %#v", l)
			}
		})
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fake", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "homonto.toml")
	write(t, path, "[repos]\nfake='fake'\n")
	if _, err := workspace.Load(path); err != nil {
		t.Fatalf("legacy .git presence contract: %v", err)
	}
	write(t, path, "schema_version=2\n[repos]\nfake='fake'\n")
	if _, err := workspace.Load(path); err == nil {
		t.Fatal("schema 2 accepted fake Git metadata")
	}
}

func TestLayoutCombinedExplicitSelf(t *testing.T) {
	root := t.TempDir()
	git(t, "init", "-q", root)
	path := filepath.Join(root, "homonto.toml")
	write(t, path, "schema_version=2\n[repos]\napp='.'\n")
	l, err := workspace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Repos["app"] != root || l.GitMode != "existing" {
		t.Fatalf("combined layout: %#v", l)
	}
	write(t, path, "schema_version=1\n[repos]\napp='.'\n")
	if _, err := workspace.Load(path); err == nil || !strings.Contains(err.Error(), "implicit") {
		t.Fatalf("legacy self = %v", err)
	}
	write(t, path, "schema_version=2\n[workflow]\ngit='managed'\n[repos]\napp='.'\n")
	if _, err := workspace.Load(path); err == nil {
		t.Fatal("managed records inside code repo accepted")
	}
}

func TestLayoutPathBoundaries(t *testing.T) {
	base := t.TempDir()
	root, repo := filepath.Join(base, "control"), filepath.Join(base, "source")
	git(t, "init", "-q", repo)
	path := filepath.Join(root, "homonto.toml")
	for _, tc := range []struct{ name, workflow, mode, worktrees, want string }{
		{"external relative", "../records", "existing", "../worktrees", ""},
		{"external absolute", filepath.Join(base, "absolute records"), "existing", "", ""},
		{"existing inside source", "../source/docs", "existing", "../worktrees", ""},
		{"filesystem root", "/", "existing", "", "workflow.root"},
		{"config root", ".", "existing", "", "configuration root"},
		{"clean config root", "docs/..", "existing", "", "configuration root"},
		{"config ancestor", "..", "existing", "", "configuration root"},
		{"control plane", ".homonto/records", "existing", "", "control plane"},
		{"opencode", ".opencode", "existing", "", "control plane"},
		{"opencode config", "opencode.jsonc", "existing", "", "control plane"},
		{"git metadata", "../source/.git/records", "existing", "", "control plane"},
		{"source root", "../source", "existing", "", "overlaps"},
		{"managed inside source", "../source/docs", "managed", "", "source repos"},
		{"worktrees source ancestor", "docs", "existing", "..", "configuration root"},
		{"worktrees inside source", "docs", "existing", "../source/trees", "source repos"},
		{"worktrees source root", "docs", "existing", "../source", "overlaps"},
		{"worktrees filesystem root", "docs", "existing", "/", "worktrees.dir"},
		{"worktrees config", "docs", "existing", ".", "configuration root"},
		{"worktrees control plane", "docs", "existing", ".opencode/trees", "control plane"},
		{"worktrees inside records", "records", "existing", "records/trees", "overlaps workflow.root"},
		{"records inside worktrees", "trees/records", "existing", "trees", "overlaps workflow.root"},
		{"wildcard", "records*", "existing", "", "invalid path"},
		{"control character", "records\nescape", "existing", "", "invalid path"},
		{"backslash", `records\escape`, "existing", "", "invalid path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf("schema_version=2\n[repos]\napp='../source'\n[workflow]\nroot=%q\ngit=%q\n[worktrees]\ndir=%q\n", tc.workflow, tc.mode, tc.worktrees)
			write(t, path, body)
			_, err := workspace.Load(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLayoutGitIdentity(t *testing.T) {
	base := t.TempDir()
	repo, linked := filepath.Join(base, "source"), filepath.Join(base, "linked")
	git(t, "init", "-q", repo)
	git(t, "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "--allow-empty", "-m", "initial")
	git(t, "-C", repo, "worktree", "add", "--detach", linked)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "control", "homonto.toml")
	for _, tc := range []struct{ name, repos, want string }{
		{"linked source", "app='../linked'", ""},
		{"alias source", "app='../alias'", ""},
		{"same canonical tree", "a='../source'\nb='../alias'", "same repository"},
		{"same common dir", "a='../source'\nb='../linked'", "same repository"},
		{"subdirectory", "app='../source/subdir'", "exact Git top-level"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(t, path, "schema_version=2\n[repos]\n"+tc.repos+"\n")
			_, err := workspace.Load(path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want %q", err, tc.want)
			}
		})
	}
	write(t, path, "schema_version=2\n[workflow]\ngit='managed'\nroot='../linked'\n")
	if _, err := workspace.Load(path); err == nil || !strings.Contains(err.Error(), "standalone") {
		t.Fatalf("managed linked tree = %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(base, "control"))
	write(t, path, "schema_version=2\n[repos]\napp='.'\n")
	if _, err := workspace.Load(path); err == nil {
		t.Fatal("Git environment spoofed a valid source")
	}
}

func TestReadGitDisablesConfiguredFSMonitor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic fsmonitor hook uses a POSIX shell")
	}
	repo := t.TempDir()
	git(t, "init", "-q", repo)
	write(t, filepath.Join(repo, "tracked"), "tracked\n")
	git(t, "-C", repo, "add", "tracked")
	git(t, "-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "initial")

	sentinel := filepath.Join(t.TempDir(), "fsmonitor-ran")
	hook := filepath.Join(t.TempDir(), "fsmonitor-hook")
	write(t, hook, fmt.Sprintf("#!/bin/sh\n: > %q\nprintf '0000000000000000000000000000000000000000\\n'\n", sentinel))
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, "-C", repo, "config", "--local", "core.fsmonitor", hook)

	if out, err := exec.Command("git", "-C", repo, "status", "--porcelain=v1").CombinedOutput(); err != nil {
		t.Fatalf("configured fsmonitor probe: %v\n%s", err, out)
	}
	if _, err := os.Lstat(sentinel); err != nil {
		t.Fatalf("configured fsmonitor hook did not run: %v", err)
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := workspace.ReadGit(repo, "status", "--porcelain=v1", "-z", "--untracked-files=all"); err != nil {
		t.Fatalf("ReadGit: %v", err)
	}
	if _, _, err := workspace.GitIdentity(repo); err != nil {
		t.Fatalf("GitIdentity: %v", err)
	}
	if _, err := os.Lstat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("fsmonitor hook ran during read-only probe: %v", err)
	}
	after, err := os.ReadFile(index)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only probe changed index: %v", err)
	}
}

func TestLayoutManagedStructuralValidationOnly(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "control", "homonto.toml")
	records := filepath.Join(base, "records")
	write(t, filepath.Join(records, "unrelated.txt"), "must survive")
	write(t, path, "schema_version=2\n[workflow]\ngit='managed'\nroot='../records'\n")
	if _, err := workspace.Load(path); err != nil {
		t.Fatalf("initializer owns adoption: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(records, ".git")); !os.IsNotExist(err) {
		t.Fatalf("loader initialized Git: %v", err)
	}
	git(t, "init", "-q", records)
	if _, err := workspace.Load(path); err != nil {
		t.Fatalf("standalone Git root: %v", err)
	}
	git(t, "init", "-q", base)
	if _, err := workspace.Load(path); err == nil || !strings.Contains(err.Error(), "inside Git repository") {
		t.Fatalf("nested managed repository: %v", err)
	}
}

func TestLayoutSymlinkAndFileBoundaries(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "control")
	path := filepath.Join(root, "homonto.toml")
	write(t, path, "schema_version=2\n")
	if err := os.Mkdir(filepath.Join(root, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".opencode"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	write(t, path, "schema_version=2\n[workflow]\nroot='alias/records'\n")
	if _, err := workspace.Load(path); err == nil || !strings.Contains(err.Error(), "control plane") {
		t.Fatalf("symlink collision: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "absent"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	write(t, path, "schema_version=2\n[workflow]\nroot='dangling/records'\n")
	if _, err := workspace.Load(path); err == nil {
		t.Fatal("dangling ancestor accepted")
	}
	write(t, filepath.Join(root, "file"), "data")
	for _, p := range []string{"file", "file/records"} {
		write(t, path, fmt.Sprintf("schema_version=2\n[workflow]\nroot=%q\n", p))
		if _, err := workspace.Load(path); err == nil {
			t.Fatalf("file boundary %q accepted", p)
		}
	}
}

func TestLayoutSchemaGuards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homonto.toml")
	if _, err := workspace.LoadRoot(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing config must remain an error: %v", err)
	}
	for _, body := range []string{"[workflow]\ngit='existing'", "schema_version=1\n[workflow]\ngit=''", "[worktrees]", "schema_version=1\n[worktrees]\ndir='../trees'"} {
		write(t, path, body)
		if _, err := workspace.Load(path); err == nil || !strings.Contains(err.Error(), "schema_version=2") {
			t.Fatalf("legacy new fields %q: %v", body, err)
		}
	}
	write(t, path, "schema_version=3\n")
	if _, err := workspace.Load(path); !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("future schema: %v", err)
	}
	for _, body := range []string{"schema_version=-1", "schema_version=2\n[workflow]\ngit=''", "schema_version=2\n[workflow]\ngit='auto'"} {
		write(t, path, body)
		if _, err := workspace.Load(path); err == nil {
			t.Fatalf("invalid schema/mode %q accepted", body)
		}
	}
}
