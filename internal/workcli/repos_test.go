package workcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeclaredRepos reads the [repos] table from the workspace's homonto.toml
// (nil without a file or table) — the context source onto/to init share.
func TestDeclaredRepos(t *testing.T) {
	root := t.TempDir()

	if got := DeclaredRepos(root); got != nil {
		t.Errorf("DeclaredRepos(no config) = %v, want nil", got)
	}

	if err := os.WriteFile(filepath.Join(root, "homonto.toml"),
		[]byte("[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DeclaredRepos(root); got != nil {
		t.Errorf("DeclaredRepos(no repos table) = %v, want nil", got)
	}

	if err := os.WriteFile(filepath.Join(root, "homonto.toml"),
		[]byte("[repos]\nservice-a = \"../service-a\"\nzz = \"/abs/zz\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := DeclaredRepos(root)
	if len(got) != 2 || got["service-a"] != "../service-a" || got["zz"] != "/abs/zz" {
		t.Errorf("DeclaredRepos = %v, want both entries verbatim", got)
	}
}

// TestRepoContextLines verifies the init context block: absent without a
// [repos] table, and otherwise one header plus name-ordered lines naming each
// declared repo and its declared path.
func TestRepoContextLines(t *testing.T) {
	root := t.TempDir()

	if got := RepoContextLines(root); got != nil {
		t.Errorf("RepoContextLines(no repos) = %v, want nil", got)
	}

	if err := os.WriteFile(filepath.Join(root, "homonto.toml"),
		[]byte("[repos]\nzz = \"/abs/zz\"\nservice-a = \"../service-a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := RepoContextLines(root)
	joined := strings.Join(lines, "\n")
	if len(lines) != 3 {
		t.Fatalf("RepoContextLines = %d lines, want header + 2 repos:\n%s", len(lines), joined)
	}
	if !strings.Contains(lines[0], "designated home") {
		t.Errorf("header must state the designated-home scope:\n%s", joined)
	}
	// Name order, not map order or declaration order.
	if !(strings.Contains(lines[1], "service-a") && strings.Contains(lines[2], "zz")) {
		t.Errorf("repos must list in name order (service-a before zz):\n%s", joined)
	}
}
