package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestWorktreeReceiverCLIAndDefaultRemovalRole(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	if err := os.Mkdir(repo, 0755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "release")
	git("config", "user.name", "Receiver Test")
	git("config", "user.email", "receiver@example.invalid")
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base")
	base := git("rev-parse", "HEAD")
	git("switch", "-c", "original-work")
	config := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(config, []byte("schema_version=2\n[workflow]\nroot='records'\n[worktrees]\ndir='execution'\n[repos]\napp='source'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := workflowroot.WriteLayoutMarker(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: config, WorkflowRoot: filepath.Join(root, "records"), GitMode: "existing"}); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "records", "tasks", "feature")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	state := fmt.Sprintf("change: feature\nid: generation-one\nphase: do\nrepo_mode: explicit\nrepos: [app]\nrepo_bases:\n  app:\n    base_ref: %q\n    base_branch: release\n    git_common_dir: %q\n", base, filepath.Join(repo, ".git"))
	statePath := filepath.Join(stateDir, "to-state.yaml")
	if err := os.WriteFile(statePath, []byte(state), 0644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append(args, "--config", config))
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var w workspace.Worktree
	if err := json.Unmarshal([]byte(out), &w); err != nil {
		t.Fatalf("JSON: %s %v", out, err)
	}
	if w.Role != "receiver" || w.BaseCommit != base || w.BaseTarget != "refs/heads/release" || w.Status != "ready" {
		t.Fatalf("receiver response: %+v", w)
	}
	if out, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app"); err != nil || strings.TrimSpace(out) != w.Path {
		t.Fatalf("path response: %q %v", out, err)
	}
	if err := os.WriteFile(statePath, []byte(strings.Replace(state, "phase: do", "phase: done", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "records", "tasks", "archive", "2026-09-08-feature")
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(stateDir, archive); err != nil {
		t.Fatal(err)
	}
	if out, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app"); err != nil || strings.TrimSpace(out) != w.Path {
		t.Fatalf("postarchive retry: %q %v", out, err)
	}
	if _, err := run("worktree", "remove", "feature", "--workflow", "to", "--repo", "app", "--yes"); err == nil {
		t.Fatal("default execution removal deleted receiver")
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := run("worktree", "remove", "feature", "--workflow", "to", "--repo", "app", "--role", "receiver"); err == nil {
		t.Fatal("removal omitted confirmation")
	}
	out, err = run("worktree", "remove", "feature", "--workflow", "to", "--repo", "app", "--role", "receiver", "--yes", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var removed workspace.Worktree
	if err := json.Unmarshal([]byte(out), &removed); err != nil || removed != w {
		t.Fatalf("removed response: %s %v", out, err)
	}
	secondArchive := filepath.Join(filepath.Dir(archive), "2026-09-09-feature")
	if err := os.MkdirAll(secondArchive, 0755); err != nil {
		t.Fatal(err)
	}
	secondState := strings.ReplaceAll(strings.Replace(state, "phase: do", "phase: done", 1), "generation-one", "generation-two")
	if err := os.WriteFile(filepath.Join(secondArchive, "to-state.yaml"), []byte(secondState), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app"); err == nil || !strings.Contains(err.Error(), "--state-id") {
		t.Fatalf("ambiguous archive accepted: %v", err)
	}
	out, err = run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app", "--state-id", "generation-two", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &w); err != nil || w.StateID != "id:generation-two" {
		t.Fatalf("explicit receiver identity: %s %v", out, err)
	}
	if out, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app", "--state-id", "id:generation-two"); err != nil || strings.TrimSpace(out) != w.Path {
		t.Fatalf("registered stateID retry: %s %v", out, err)
	}
	if _, err := run("worktree", "receiver", "feature", "--workflow", "to", "--repo", "app", "--state-id", "generation-one"); err == nil {
		t.Fatal("explicit ID rebound receiver to another generation")
	}
}
