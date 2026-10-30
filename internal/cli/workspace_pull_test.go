package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestWorkspacePullCLIFastForwardsBase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	remote := filepath.Join(root, "remote.git")
	git(root, "init", "--quiet", "--bare", "-b", "main", remote)
	clone := func(name string) string {
		dir := filepath.Join(root, name)
		git(root, "clone", "--quiet", remote, dir)
		git(dir, "config", "user.name", "Pull CLI Test")
		git(dir, "config", "user.email", "pull@example.invalid")
		git(dir, "config", "commit.gpgsign", "false")
		return dir
	}
	upstream := clone("upstream")
	git(upstream, "commit", "--quiet", "--allow-empty", "-m", "base")
	git(upstream, "push", "--quiet", "origin", "main")
	source := clone("source")
	git(upstream, "commit", "--quiet", "--allow-empty", "-m", "newer")
	git(upstream, "push", "--quiet", "origin", "main")
	want := git(upstream, "rev-parse", "HEAD")

	config := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(config, []byte("schema_version=2\n[workflow]\nroot='records'\n[repos]\napp='source'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"workspace", "pull", "--repo", "app", "--branch", "main", "--json", "--config", config})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("workspace pull: %v\n%s", err, out.String())
	}
	var got workspace.PullResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if got.Status != "fast-forwarded" || got.After != want || got.Repo != "app" {
		t.Fatalf("result = %+v, want app fast-forwarded to %s", got, want)
	}
	if head := git(source, "rev-parse", "HEAD"); head != want {
		t.Fatalf("source HEAD = %s, want %s", head, want)
	}
}
