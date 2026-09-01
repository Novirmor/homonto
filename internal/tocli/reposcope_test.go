package tocli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "tracked"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "init")
}

// TestRequireCleanScopeOnlyAuditsSelectedRepos proves the terminal gate
// ignores an unrelated declared sibling and refuses a selected dirty repo.
func TestRequireCleanScopeOnlyAuditsSelectedRepos(t *testing.T) {
	base := t.TempDir()
	root, api, web := filepath.Join(base, "config"), filepath.Join(base, "api"), filepath.Join(base, "web")
	for _, dir := range []string{root, api, web} {
		initRepo(t, dir)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("[repos]\napi = \"../api\"\nweb = \"../web\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "repos")
	if err := os.WriteFile(filepath.Join(web, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireCleanScope(root, []string{"api"}); err != nil {
		t.Fatalf("unselected dirty repo blocked terminal gate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireCleanScope(root, []string{"api"}); err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("selected dirty repo gate = %v, want api dirt error", err)
	}
}
