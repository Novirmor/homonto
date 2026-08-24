package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/gitx"
)

func TestCreateControlRepository(t *testing.T) {
	root := t.TempDir()
	canon := CanonicalPathOf(t, root)
	app := initRepo(t, filepath.Join(root, "app"))
	writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")

	ctl, err := CreateControlRepository(context.Background(), canon, []Candidate{
		{Path: canon, Kind: "git"}, // control itself: never self-ignored
		{Path: CanonicalPathOf(t, filepath.Join(root, "docs")), Kind: "non_git"},
		{Path: app, Kind: "git"},
	}, gitx.ExecRunner{})
	if err != nil {
		t.Fatalf("CreateControlRepository: %v", err)
	}

	if ctl.Root != canon {
		t.Errorf("Control.Root = %q, want %q", ctl.Root, canon)
	}
	if ctl.Git.TopLevel != canon {
		t.Errorf("Control.Git.TopLevel = %q, want %q", ctl.Git.TopLevel, canon)
	}
	if want := filepath.Join(canon, ".git"); ctl.Git.CommonDir != want {
		t.Errorf("Control.Git.CommonDir = %q, want %q", ctl.Git.CommonDir, want)
	}

	wantIgnore := "# homonto runtime state\n.homonto/\n\n# homonto workspace members\napp/\ndocs/\n"
	got, err := os.ReadFile(filepath.Join(canon, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if string(got) != wantIgnore {
		t.Errorf(".gitignore = %q, want %q", got, wantIgnore)
	}

	r := gitx.ExecRunner{}
	out, err := r.Run(context.Background(), canon, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if strings.TrimSpace(out) != "1" {
		t.Errorf("commit count = %q, want exactly 1", strings.TrimSpace(out))
	}
	status, err := r.Run(context.Background(), canon, "status", "--porcelain")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.TrimSpace(status) != "" {
		t.Errorf("working tree not clean after creation:\n%s", status)
	}
	head, err := r.Run(context.Background(), canon, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if strings.TrimSpace(head) != "main" {
		t.Errorf("HEAD = %q, want main", strings.TrimSpace(head))
	}
}

func TestCreateControlRepositoryWithoutMembers(t *testing.T) {
	canon := CanonicalPathOf(t, t.TempDir())
	if _, err := CreateControlRepository(context.Background(), canon, nil, gitx.ExecRunner{}); err != nil {
		t.Fatalf("CreateControlRepository: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(canon, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	want := "# homonto runtime state\n.homonto/\n"
	if string(got) != want {
		t.Errorf(".gitignore = %q, want %q", got, want)
	}
}

func TestCreateControlRepositoryRejectsExistingGit(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	if _, err := CreateControlRepository(context.Background(), root, nil, gitx.ExecRunner{}); err == nil {
		t.Fatal("CreateControlRepository: expected error for already-initialized root")
	}
}

func TestCreateControlRepositoryMissingRoot(t *testing.T) {
	if _, err := CreateControlRepository(context.Background(), filepath.Join(t.TempDir(), "nope"), nil, gitx.ExecRunner{}); err == nil {
		t.Fatal("CreateControlRepository: expected error for missing root")
	}
}

func TestCreateControlRepositoryRejectsForeignMember(t *testing.T) {
	root := CanonicalPathOf(t, t.TempDir())
	foreign := initRepo(t, filepath.Join(t.TempDir(), "foreign"))
	if _, err := CreateControlRepository(context.Background(), root, []Candidate{{Path: foreign, Kind: "git"}}, gitx.ExecRunner{}); err == nil {
		t.Fatal("CreateControlRepository: expected error for member outside root")
	}
}
