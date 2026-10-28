//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/worktask"
)

// interruptOnOutput sends this process SIGINT once the check starts writing,
// as a terminal would to homonto, which is the foreground process group.
type interruptOnOutput struct {
	once sync.Once
	err  error
}

func (w *interruptOnOutput) Write(p []byte) (int, error) {
	w.once.Do(func() { w.err = syscall.Kill(os.Getpid(), syscall.SIGINT) })
	return len(p), nil
}

func TestWorkspaceTaskCheckInterruptCancelsAndRecords(t *testing.T) {
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
[integration.a-slow]
repos=['app']
repo='app'
cwd='.'
command=['sh','-c','echo ready; sleep 30']
[integration.b-next]
repos=['app']
repo='app'
cwd='.'
command=['true']
`
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(stderr *interruptOnOutput, args ...string) (string, error) {
		t.Helper()
		cmd := NewRootCmd()
		var stdout bytes.Buffer
		cmd.SetOut(&stdout)
		if stderr != nil {
			cmd.SetErr(stderr)
		}
		cmd.SetArgs(append([]string{"workspace", "task"}, append(args, "--config", path)...))
		err := cmd.Execute()
		return stdout.String(), err
	}
	if _, err := run(nil, "create", "feature", "--engine", "no", "--repo", "app", "--isolation", "existing"); err != nil {
		t.Fatal(err)
	}
	interrupt := &interruptOnOutput{}
	started := time.Now()
	if _, err := run(interrupt, "check", "feature", "--writers-paused"); err == nil {
		t.Fatal("interrupted check reported success")
	}
	if interrupt.err != nil {
		t.Fatal(interrupt.err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("interrupt did not stop the check promptly: %s", elapsed)
	}
	out, err := run(nil, "inspect", "feature")
	if err != nil {
		t.Fatal(err)
	}
	var task worktask.Task
	if err := json.Unmarshal([]byte(out), &task); err != nil {
		t.Fatal(err)
	}
	if r := task.Results["a-slow"]; r.Status != "failed" || r.Finished.IsZero() {
		t.Fatalf("interrupted run must record a finished failure, got %+v", r)
	}
	if _, ran := task.Results["b-next"]; ran {
		t.Fatal("checks continued after the interrupt")
	}
}
