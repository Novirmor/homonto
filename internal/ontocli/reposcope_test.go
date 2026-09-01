package ontocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScopedWorktreeDirt audits only the config repo and aliases recorded on
// the change. A dirty unselected declared repo must not block another change.
func TestScopedWorktreeDirt(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "config")
	api := filepath.Join(base, "api")
	web := filepath.Join(base, "web")
	for _, dir := range []string{root, api, web} {
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
	writeFile(t, filepath.Join(root, "homonto.toml"), "[repos]\napi = \"../api\"\nweb = \"../web\"\n")
	commitAll(t, root, "repos")
	dirtyWorktree(t, web) // declared, but not selected for this change.

	repos, err := scopedWorktreeDirt(root, "change", []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if got := scopedDirtGateError(repos, "change"); got != "" {
		t.Fatalf("unselected dirty repo blocked scope: %s", got)
	}
	dirtyWorktree(t, api)
	repos, err = scopedWorktreeDirt(root, "change", []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if got := scopedDirtGateError(repos, "change"); !strings.Contains(got, "api:") {
		t.Fatalf("selected repo dirt must be labelled api, got %q", got)
	}
}

// TestScopeDirsRejectsRemovedAlias keeps stored names fail-closed rather than
// silently dropping a repository from a close gate.
func TestScopeDirsRejectsRemovedAlias(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "homonto.toml"), "[repos]\n")
	_, _, err := scopeDirs(root, []string{"gone"})
	if err == nil || !strings.Contains(err.Error(), "no longer declared") {
		t.Fatalf("scopeDirs removed alias = %v, want a declaration error", err)
	}
}
