package workspace_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/cli"
	"github.com/noviopenworks/homonto/internal/ontocli"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tocli"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func commandLayout(t *testing.T) workspace.Layout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Command Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "command@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "Command Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "command@example.test")
	root := t.TempDir()
	commandGit(t, root, "init", "source")
	commandWrite(t, filepath.Join(root, "source", "source.txt"), "source\n")
	commandGit(t, filepath.Join(root, "source"), "add", "source.txt")
	commandGit(t, filepath.Join(root, "source"), "commit", "-m", "source seed")
	commandWrite(t, filepath.Join(root, "homonto.toml"), "schema_version = 2\n[workflow]\nroot = 'records'\ngit = 'managed'\n[repos]\nsource = 'source'\n[frameworks.onto]\n[frameworks.to]\n")
	for _, framework := range []string{"onto", "to"} {
		if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", framework), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func commandWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commandGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func commandDisk(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		files[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func executeCommand(root *cobra.Command, args ...string) (string, error) {
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func requireCommand(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	out, err := executeCommand(root, args...)
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", root.Name(), args, err, out)
	}
	return out
}

// Enumerate real registrations so a new command needs an explicit classification
// here, rather than silently escaping coverage through a synthetic command tree.
func TestCommandHistoryClassification(t *testing.T) {
	for _, workflow := range []struct {
		name             string
		root             func() *cobra.Command
		writers, readers []string
	}{
		{"onto", ontocli.NewRootCmd,
			[]string{"init", "new", "advance", "bypass", "close", "complete-integration", "abandon", "demote", "merge-deltas", "evidence record",
				"set isolation", "set integration", "set build-mode", "set tdd-mode", "set verify-scale", "set verify-result", "set build-pause",
				"set proposal-approved", "set approach-confirmed", "set close-confirmed", "set close-merged", "set directive", "set base-ref",
				"set base-branch", "set workflow", "set deps", "set supersedes", "set deviates-from", "set guides"},
			[]string{"version", "status", "graph", "doctor", "state", "gate", "dirt", "trace"}},
		{"to", tocli.NewRootCmd,
			[]string{"init", "new", "phase", "bypass", "done", "abandon", "promote"},
			[]string{"version", "status", "doctor"}},
	} {
		t.Run(workflow.name, func(t *testing.T) {
			l := commandLayout(t)
			before := commandDisk(t, l.ConfigRoot)
			classes := map[string]bool{}
			for _, path := range workflow.writers {
				classes[path] = true
			}
			for _, path := range workflow.readers {
				classes[path] = false
			}
			classes["handoff"] = false
			if workflow.name == "onto" {
				classes["scale"] = false
			}
			seen := map[string]bool{}
			var visit func(*cobra.Command, string)
			visit = func(cmd *cobra.Command, path string) {
				for _, child := range cmd.Commands() {
					visit(child, strings.TrimSpace(path+" "+child.Name()))
				}
				if cmd.RunE == nil {
					return
				}
				writer, ok := classes[path]
				if !ok {
					t.Errorf("unclassified command %q", path)
					return
				}
				seen[path] = true
				variants := [][]string{nil}
				flag := ""
				if path == "handoff" || path == "scale" {
					flag = "write"
					if path == "scale" {
						flag = "set"
					}
					variants = [][]string{nil, {"--json"}, {"--" + flag}, {"--" + flag, "--json"}, {"--" + flag + "=false"}, {"--" + flag + "=false", "--json"}, {"--" + flag, "--json=false"}}
				} else if cmd.Flags().Lookup("json") != nil {
					variants = append(variants, []string{"--json"})
				}
				for _, flags := range variants {
					t.Run(path+" "+strings.Join(flags, " "), func(t *testing.T) {
						root := workflow.root()
						leaf, _, err := root.Find(strings.Fields(path))
						if err != nil {
							t.Fatal(err)
						}
						if leaf.Flags().Lookup("dir") != nil {
							flags = append(append([]string{}, flags...), "--dir", l.ConfigRoot)
						}
						if err := leaf.ParseFlags(flags); err != nil {
							t.Fatal(err)
						}
						wantWriter := writer
						if flag != "" {
							wantWriter, _ = leaf.Flags().GetBool(flag)
							jsonMode, _ := leaf.Flags().GetBool("json")
							if workflow.name == "onto" && path == "handoff" && jsonMode {
								wantWriter = false
							}
						}
						var out bytes.Buffer
						root.SetOut(&out)
						root.SetErr(&out)
						// Call the logical handler directly: argument validation is
						// unrelated, and missing records keep read-only probes small.
						err = leaf.RunE(leaf, []string{"demo", "direct"})
						blocked := err != nil && strings.Contains(err.Error(), "homonto workspace init --yes")
						if blocked != wantWriter {
							t.Fatalf("history gated = %t, want %t: %v\n%s", blocked, wantWriter, err, out.String())
						}
						if !reflect.DeepEqual(before, commandDisk(t, l.ConfigRoot)) {
							t.Fatal("read-only or blocked command wrote files")
						}
					})
				}
			}
			visit(workflow.root(), "")
			if len(seen) != len(classes) {
				t.Fatalf("registered commands = %v; classifications = %v", seen, classes)
			}
		})
	}
}

func TestCommandHistoryManagedRoots(t *testing.T) {
	l := commandLayout(t)
	before := commandDisk(t, l.ConfigRoot)
	for _, root := range []*cobra.Command{ontocli.NewRootCmd(), tocli.NewRootCmd()} {
		if _, err := executeCommand(root, "init", "--dir", l.ConfigRoot); err == nil || !strings.Contains(err.Error(), "homonto workspace init --yes") {
			t.Fatalf("uninitialized %s init: %v", root.Name(), err)
		}
	}
	requireCommand(t, cli.NewRootCmd(), "workspace", "inspect", "--json", "--config", l.ConfigPath)
	requireCommand(t, cli.NewRootCmd(), "workflow", "snapshot", "--json", "--config", l.ConfigPath)
	requireCommand(t, cli.NewRootCmd(), "worktree", "list", "--json", "--config", l.ConfigPath)
	if !reflect.DeepEqual(before, commandDisk(t, l.ConfigRoot)) {
		t.Fatal("inspection or refused init wrote files")
	}
	if _, err := executeCommand(cli.NewRootCmd(), "workspace", "init", "--config", l.ConfigPath); err == nil {
		t.Fatal("workspace init did not require --yes")
	}
	requireCommand(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
	for _, root := range []*cobra.Command{ontocli.NewRootCmd(), tocli.NewRootCmd()} {
		requireCommand(t, root, "init", "--dir", l.ConfigRoot)
	}
	if got := commandGit(t, l.WorkflowRoot, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("directory-only init created empty commits: %s", got)
	}
	// Exercise --dir relative to the process cwd, without rewriting the flag
	// passed to the handler or accidentally using cwd as the configuration root.
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chdir(filepath.Dir(l.ConfigRoot)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Base(l.ConfigRoot)
	for _, step := range []struct {
		root        *cobra.Command
		args        []string
		label, path string
	}{
		{ontocli.NewRootCmd(), []string{"new", "alpha", "--repo", "source"}, "onto new", "changes/alpha/onto-state.yaml"},
		{ontocli.NewRootCmd(), []string{"set", "build-mode", "alpha", "direct"}, "onto set build-mode", "changes/alpha/onto-state.yaml"},
		{tocli.NewRootCmd(), []string{"new", "beta", "--repo", "source", "--json"}, "to new", "tasks/beta/to-state.yaml"},
		{tocli.NewRootCmd(), []string{"phase", "beta", "--json"}, "to phase", "tasks/beta/to-state.yaml"},
		{ontocli.NewRootCmd(), []string{"handoff", "alpha", "--write"}, "onto handoff", "changes/alpha/onto-state.yaml"},
		{tocli.NewRootCmd(), []string{"handoff", "beta", "--write", "--json"}, "to handoff", "tasks/beta/to-state.yaml"},
	} {
		head := commandGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
		requireCommand(t, step.root, append(step.args, "--dir", dir)...)
		if got := commandGit(t, l.WorkflowRoot, "log", "-1", "--format=%s"); got != step.label {
			t.Fatalf("commit label = %q, want %q", got, step.label)
		}
		if got := commandGit(t, l.WorkflowRoot, "rev-list", "--count", head+"..HEAD"); got != "1" {
			t.Fatalf("logical command created %s commits, want 1", got)
		}
		commandGit(t, l.WorkflowRoot, "show", "HEAD:"+step.path)
	}
	if got := commandGit(t, l.WorkflowRoot, "status", "--porcelain"); got != "" {
		t.Fatalf("uncommitted records: %s", got)
	}
	if err := os.Chdir(l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	head := commandGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	requireCommand(t, ontocli.NewRootCmd(), "set", "build-mode", "alpha", "direct")
	if got := commandGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
		t.Fatal("default --dir no-op created an empty commit")
	}
	for _, path := range []string{"workspace checkpoint", "workspace recover", "worktree create", "worktree remove", "snapshot", "workflow snapshot"} {
		cmd, rest, err := cli.NewRootCmd().Find(strings.Fields(path))
		if err != nil || len(rest) != 0 || cmd.Name() != filepath.Base(strings.ReplaceAll(path, " ", "/")) {
			t.Fatalf("missing homonto command %q: %v, %v", path, rest, err)
		}
	}
}

func TestCommandHistoryArchiveAndConversionWriteSets(t *testing.T) {
	l := commandLayout(t)
	requireCommand(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
	requireCommand(t, tocli.NewRootCmd(), "init", "--dir", l.ConfigRoot)
	for generation := 0; generation < 2; generation++ {
		requireCommand(t, tocli.NewRootCmd(), "new", "recurring", "--repo", "source", "--dir", l.ConfigRoot)
		requireCommand(t, tocli.NewRootCmd(), "phase", "recurring", "--dir", l.ConfigRoot)
		requireCommand(t, tocli.NewRootCmd(), "done", "recurring", "--verified", "--dir", l.ConfigRoot)
		paths := commandGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "--no-renames", "HEAD")
		for _, p := range strings.Split(paths, "\n") {
			if !strings.HasPrefix(p, "tasks/recurring/") && !strings.HasPrefix(p, "tasks/archive/") {
				t.Fatalf("archive captured unrelated path: %q", p)
			}
		}
		if !strings.Contains(paths, "tasks/archive/") || !strings.Contains(paths, "tasks/recurring/to-state.yaml") {
			t.Fatalf("archive did not record both endpoints: %s", paths)
		}
		if dirt := commandGit(t, l.WorkflowRoot, "status", "--porcelain"); dirt != "" {
			t.Fatalf("archive writes missing from history: %s", dirt)
		}
	}
	requireCommand(t, ontocli.NewRootCmd(), "new", "alpha", "--repo", "source", "--dir", l.ConfigRoot)
	requireCommand(t, ontocli.NewRootCmd(), "demote", "alpha", "--as", "tiny", "--yes", "--dir", l.ConfigRoot)
	paths := commandGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "--no-renames", "HEAD")
	for _, p := range strings.Split(paths, "\n") {
		if !strings.HasPrefix(p, "changes/alpha/") && !strings.HasPrefix(p, "tasks/tiny/") {
			t.Fatalf("conversion captured unrelated path: %q", p)
		}
	}
	if !strings.Contains(paths, ".workflow/") {
		t.Fatalf("conversion provenance missing: %s", paths)
	}
	requireCommand(t, tocli.NewRootCmd(), "promote", "tiny", "--as", "alpha", "--yes", "--dir", l.ConfigRoot)
	if dirt := commandGit(t, l.WorkflowRoot, "status", "--porcelain"); dirt != "" {
		t.Fatalf("conversion writes missing from history: %s", dirt)
	}
}

func TestCommandHistoryAdvancePartialProgress(t *testing.T) {
	l := commandLayout(t)
	requireCommand(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
	requireCommand(t, ontocli.NewRootCmd(), "new", "partial", "--workflow", "fix", "--repo", "source", "--dir", l.ConfigRoot)
	head := commandGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	out, err := executeCommand(ontocli.NewRootCmd(), "advance", "partial", "--to", "build", "--dir", l.ConfigRoot)
	if err == nil || !strings.Contains(err.Error(), "isolation") || !strings.Contains(out, "design") {
		t.Fatalf("expected first hop then isolation failure: %v\n%s", err, out)
	}
	if got := commandGit(t, l.WorkflowRoot, "rev-list", "--count", head+"..HEAD"); got != "1" {
		t.Fatalf("multi-hop command created %s commits, want 1", got)
	}
	if got := commandGit(t, l.WorkflowRoot, "show", "HEAD:changes/partial/onto-state.yaml"); !strings.Contains(got, "phase: design") {
		t.Fatalf("first hop not committed: %s", got)
	}
	if got := commandGit(t, l.WorkflowRoot, "log", "-1", "--format=%s"); got != "onto advance" {
		t.Fatalf("wrong logical operation label: %q", got)
	}
}

func TestCommandHistoryPartialProgressAndRecovery(t *testing.T) {
	for _, hookFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(hookFailure), func(t *testing.T) {
			l := commandLayout(t)
			requireCommand(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
			head := commandGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
			var hook string
			if hookFailure {
				if runtime.GOOS == "windows" {
					t.Skip("test hook requires a POSIX shell")
				}
				hook = filepath.Join(l.WorkflowRoot, ".git", "hooks", "pre-commit")
				commandWrite(t, hook, "#!/bin/sh\nexit 1\n")
				if err := os.Chmod(hook, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			opErr := errors.New("second hop failed")
			root := &cobra.Command{Use: "onto", SilenceUsage: true, SilenceErrors: true}
			advance := &cobra.Command{Use: "advance", RunE: func(cmd *cobra.Command, _ []string) error {
				commandWrite(t, filepath.Join(l.WorkflowRoot, "changes", "partial", "onto-state.yaml"), "durable first hop")
				cmd.Println("completed first hop")
				return opErr
			}}
			advance.Flags().String("dir", l.ConfigRoot, "workspace root")
			root.AddCommand(advance)
			workspace.AttachHistory(root, "onto")
			out, err := executeCommand(root, "advance", "partial")
			if !errors.Is(err, opErr) || !strings.Contains(out, "completed first hop") {
				t.Fatalf("lost partial progress or callback error: %v, %q", err, out)
			}
			if hookFailure {
				if !strings.Contains(err.Error(), "homonto workspace recover") {
					t.Fatalf("lost history failure: %v", err)
				}
				if err := os.Remove(hook); err != nil {
					t.Fatal(err)
				}
				requireCommand(t, cli.NewRootCmd(), "workspace", "recover", "--config", l.ConfigPath)
			}
			if got := commandGit(t, l.WorkflowRoot, "rev-list", "--count", head+"..HEAD"); got != "1" {
				t.Fatalf("partial operation created %s commits", got)
			}
			if got := commandGit(t, l.WorkflowRoot, "show", "HEAD:changes/partial/onto-state.yaml"); got != "durable first hop" {
				t.Fatalf("partial progress not committed: %q", got)
			}
		})
	}
}

func TestCommandHistoryHookFailureIsNotSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test hook requires a POSIX shell")
	}
	l := commandLayout(t)
	requireCommand(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
	hook := filepath.Join(l.WorkflowRoot, ".git", "hooks", "pre-commit")
	commandWrite(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand(ontocli.NewRootCmd(), "new", "alpha", "--repo", "source", "--dir", l.ConfigRoot)
	if err == nil || !strings.Contains(err.Error(), "operation completed, but workflow history failed") || !strings.Contains(err.Error(), "homonto workspace recover") {
		t.Fatalf("success output must not mask history failure: %v\n%s", err, out)
	}
	if _, err := ontostate.Load(filepath.Join(l.WorkflowRoot, "changes", "alpha", "onto-state.yaml")); err != nil {
		t.Fatalf("durable state lost: %v", err)
	}
	if _, err := executeCommand(tocli.NewRootCmd(), "new", "beta", "--dir", l.ConfigRoot); err == nil || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("pending history must block the other workflow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(l.WorkflowRoot, "tasks", "beta")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("pending mutation created records: %v", err)
	}
	before := commandDisk(t, l.ConfigRoot)
	requireCommand(t, cli.NewRootCmd(), "workspace", "inspect", "--json", "--config", l.ConfigPath)
	requireCommand(t, ontocli.NewRootCmd(), "state", "alpha", "--json", "--dir", l.ConfigRoot)
	if !reflect.DeepEqual(before, commandDisk(t, l.ConfigRoot)) {
		t.Fatal("inspection modified files while history was pending")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	requireCommand(t, cli.NewRootCmd(), "workspace", "recover", "--config", l.ConfigPath)
	requireCommand(t, tocli.NewRootCmd(), "new", "beta", "--repo", "source", "--dir", l.ConfigRoot)

	lock, err := applylock.AcquireProcess(filepath.Join(l.WorkflowRoot, ".git", "homonto-history"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	before = commandDisk(t, l.ConfigRoot)
	for _, root := range []*cobra.Command{ontocli.NewRootCmd(), tocli.NewRootCmd()} {
		requireCommand(t, root, "status", "--dir", l.ConfigRoot)
	}
	requireCommand(t, ontocli.NewRootCmd(), "handoff", "alpha", "--json", "--write", "--dir", l.ConfigRoot)
	requireCommand(t, tocli.NewRootCmd(), "handoff", "beta", "--json", "--write=false", "--dir", l.ConfigRoot)
	if !reflect.DeepEqual(before, commandDisk(t, l.ConfigRoot)) {
		t.Fatal("read-only command acquired a lock or wrote files")
	}
	if _, err := executeCommand(tocli.NewRootCmd(), "phase", "beta", "--dir", l.ConfigRoot); err == nil {
		t.Fatal("mutation ignored shared history lock")
	}
	if _, err := executeCommand(tocli.NewRootCmd(), "handoff", "beta", "--json", "--write", "--dir", l.ConfigRoot); err == nil {
		t.Fatal("to handoff --json --write ignored shared history lock")
	}
}
