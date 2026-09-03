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
	if err := requireCleanScope(root, "demo", []string{"api"}); err != nil {
		t.Fatalf("unselected dirty repo blocked terminal gate: %v", err)
	}
	if err := os.WriteFile(filepath.Join(api, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireCleanScope(root, "demo", []string{"api"}); err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("selected dirty repo gate = %v, want api dirt error", err)
	}
}

func TestRequireCleanScopeAllowsCurrentVerificationRecord(t *testing.T) {
	base := t.TempDir()
	root, api := filepath.Join(base, "config"), filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		initRepo(t, dir)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("[repos]\napi = \"../api\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "repos")
	plan := filepath.Join(root, "docs", "tasks", "demo", "plan.md")
	if err := os.MkdirAll(filepath.Dir(plan), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plan, []byte("## Verification\n\npassed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := requireCleanScope(root, "demo", []string{"api"}); err != nil {
		t.Fatalf("current change verification record blocked done: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := requireCleanScope(root, "demo", []string{"api"}); err == nil || !strings.Contains(err.Error(), "config repo") {
		t.Fatalf("unrelated config dirt must still block done, got %v", err)
	}
}

func TestRequireCleanScopeRejectsRenameIntoCurrentWorkspace(t *testing.T) {
	base := t.TempDir()
	root, api := filepath.Join(base, "config"), filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		initRepo(t, dir)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("[repos]\napi = \"../api\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "repos and source")
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "mv", "source.txt", "docs/tasks/demo/evidence.txt")

	if err := requireCleanScope(root, "demo", []string{"api"}); err == nil || !strings.Contains(err.Error(), "config repo") {
		t.Fatalf("rename from outside into ignored workspace must block done, got %v", err)
	}
}

func TestRequireCleanScopeAllowsRenameWithinCurrentWorkspace(t *testing.T) {
	base := t.TempDir()
	root, api := filepath.Join(base, "config"), filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		initRepo(t, dir)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("[repos]\napi = \"../api\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "docs", "tasks", "demo")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "before.txt"), []byte("evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "workspace")
	git(t, root, "mv", "docs/tasks/demo/before.txt", "docs/tasks/demo/after.txt")

	if err := requireCleanScope(root, "demo", []string{"api"}); err != nil {
		t.Fatalf("rename wholly within current workspace should remain allowed: %v", err)
	}
}
