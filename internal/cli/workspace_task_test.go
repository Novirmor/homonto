package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/worktask"
)

func TestWorkspaceTaskCLI(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "Task Test"}, {"config", "user.email", "task@example.invalid"}, {"-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	path := filepath.Join(root, "homonto.toml")
	text := `schema_version=2
[repos]
app='source'
[engines.no]
source='builtin:no'
scope='project'
[subagents.no-implementer.opencode]
model='test/model'
[subagents.no-reviewer.opencode]
model='test/model'
[integration.a-fail]
repos=['app']
repo='app'
cwd='.'
command=['sh','-c','echo failed; exit 3']
[integration.b-pass]
repos=['app']
repo='app'
cwd='.'
command=['sh','-c','test -f "$HOMONTO_INTEGRATION_CONTEXT" && echo passed']
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, string, error) {
		t.Helper()
		cmd := NewRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"workspace", "task"}, append(args, "--config", path)...))
		err := cmd.Execute()
		return stdout.String(), stderr.String(), err
	}
	if _, _, err := run("create", "feature", "--engine", "no", "--repo", "app"); err == nil {
		t.Fatal("missing isolation accepted")
	}
	out, _, err := run("create", "feature", "--engine", "no", "--repo", "app", "--isolation", "existing")
	if err != nil {
		t.Fatal(err)
	}
	var task worktask.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil || task.Engine != "no" || task.Checkouts["app"].Path != repo {
		t.Fatalf("create JSON = %s, %v", out, err)
	}
	if _, _, err := run("check", "feature"); err == nil {
		t.Fatal("unchecked writer pause accepted")
	}
	_, stderr, err := run("check", "feature", "--writers-paused")
	if err == nil || !strings.Contains(stderr, "failed") || !strings.Contains(stderr, "passed") {
		t.Fatalf("checks must continue and retain failure: %s, %v", stderr, err)
	}
	out, _, err = run("inspect", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &task); err != nil || task.Results["a-fail"].Status != "failed" || task.Results["b-pass"].Status != "passed" {
		t.Fatalf("inspect JSON = %s, %v", out, err)
	}
	if _, _, err := run("check", "feature", "--writers-paused", "--check", "b-pass"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run("check", "feature", "--writers-paused", "--check", "unknown"); err == nil {
		t.Fatal("unknown check accepted")
	}
	if _, _, err := run("cleanup", "feature", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatal("cleanup removed existing checkout")
	}
}
