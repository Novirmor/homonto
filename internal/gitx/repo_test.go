package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// initRepo initializes a real git repository at dir with branch main.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	r := ExecRunner{}
	if err := Init(context.Background(), r, dir); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
}

func physical(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("eval symlinks %s: %v", path, err)
	}
	return p
}

func TestInitCreatesMainBranchRepository(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	r := ExecRunner{}
	out, err := r.Run(context.Background(), dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if want := "main\n"; out != want {
		t.Errorf("HEAD = %q, want %q", out, want)
	}
}

func TestInspectNonRepository(t *testing.T) {
	dir := t.TempDir()
	repo, isGit, err := Inspect(context.Background(), ExecRunner{}, dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if isGit {
		t.Error("isGit = true for plain directory, want false")
	}
	if repo.TopLevel != "" || repo.CommonDir != "" || repo.Remotes != nil {
		t.Errorf("repo = %+v, want zero value for non-repository", repo)
	}
}

func TestInspectRepository(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := ExecRunner{}
	if _, err := r.Run(context.Background(), dir, "remote", "add", "origin", "https://example.com/repo.git"); err != nil {
		t.Fatalf("remote add: %v", err)
	}
	if _, err := r.Run(context.Background(), dir, "remote", "add", "upstream", "https://example.com/up.git"); err != nil {
		t.Fatalf("remote add: %v", err)
	}

	repo, isGit, err := Inspect(context.Background(), r, dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !isGit {
		t.Fatal("isGit = false, want true")
	}
	phys := physical(t, dir)
	if repo.TopLevel != phys {
		t.Errorf("TopLevel = %q, want %q", repo.TopLevel, phys)
	}
	if want := filepath.Join(phys, ".git"); repo.CommonDir != want {
		t.Errorf("CommonDir = %q, want %q", repo.CommonDir, want)
	}
	wantRemotes := []Remote{
		{Name: "origin", URL: "https://example.com/repo.git"},
		{Name: "upstream", URL: "https://example.com/up.git"},
	}
	if len(repo.Remotes) != len(wantRemotes) {
		t.Fatalf("Remotes = %+v, want %+v", repo.Remotes, wantRemotes)
	}
	for i, rm := range repo.Remotes {
		if rm != wantRemotes[i] {
			t.Errorf("Remotes[%d] = %+v, want %+v", i, rm, wantRemotes[i])
		}
	}
}

func TestInspectFromSubdirectory(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	repo, isGit, err := Inspect(context.Background(), ExecRunner{}, sub)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !isGit {
		t.Fatal("isGit = false from subdirectory, want true")
	}
	if want := physical(t, dir); repo.TopLevel != want {
		t.Errorf("TopLevel = %q, want %q", repo.TopLevel, want)
	}
}

func TestInspectWorktreeUsesCommonDir(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := ExecRunner{}
	// A commit is required before a worktree can be attached.
	if _, err := r.Run(context.Background(), dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "seed"); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := r.Run(context.Background(), dir, "worktree", "add", wt); err != nil {
		t.Fatalf("worktree add: %v", err)
	}

	repo, isGit, err := Inspect(context.Background(), r, wt)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !isGit {
		t.Fatal("isGit = false for worktree, want true")
	}
	if want := physical(t, wt); repo.TopLevel != want {
		t.Errorf("TopLevel = %q, want %q", repo.TopLevel, want)
	}
	mainCommon := filepath.Join(physical(t, dir), ".git")
	if repo.CommonDir != mainCommon {
		t.Errorf("CommonDir = %q, want shared main common dir %q", repo.CommonDir, mainCommon)
	}
}

func TestInspectBrokenGitDirIsNegative(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	_, isGit, err := Inspect(context.Background(), ExecRunner{}, dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if isGit {
		t.Error("isGit = true for corrupt .git directory, want false")
	}
}
