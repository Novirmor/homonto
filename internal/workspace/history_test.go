package workspace

import (
	"encoding/json"
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
	"time"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/spf13/cobra"
)

func historyTestLayout(t *testing.T) Layout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_TEMPLATE_DIR", "GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "History Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "history@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "History Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "history@example.test")
	root := t.TempDir()
	historyWrite(t, filepath.Join(root, "homonto.toml"), "schema_version = 2\n[workflow]\nroot = 'records'\ngit = 'managed'\n")
	l, err := LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func historyWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func historyTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func historyInitTest(t *testing.T) Layout {
	t.Helper()
	l := historyTestLayout(t)
	if err := InitManaged(l); err != nil {
		t.Fatal(err)
	}
	return l
}

func historyDisk(t *testing.T, root string) map[string]string {
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

func TestHistoryInitAndInspectReadOnly(t *testing.T) {
	l := historyTestLayout(t)
	before := historyDisk(t, l.ConfigRoot)
	view, err := InspectHistory(l)
	if err != nil || view.Initialized || view.Pending {
		t.Fatalf("uninitialized inspect = %+v, %v", view, err)
	}
	if !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
		t.Fatal("inspection wrote files")
	}
	if err := InitManaged(l); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "ls-files"); got != ".gitignore\n.homonto-workflow.json" {
		t.Fatalf("initial tracked files = %q", got)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "log", "-1", "--format=%an <%ae>"); got != "History Test <history@example.test>" {
		t.Fatalf("commit identity = %q", got)
	}
	before = historyDisk(t, l.ConfigRoot)
	if err := InitManaged(l); err != nil {
		t.Fatalf("idempotent init: %v", err)
	}
	view, err = InspectHistory(l)
	if err != nil || !view.Initialized || view.Pending || view.Head == "" {
		t.Fatalf("initialized inspect = %+v, %v", view, err)
	}
	if !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
		t.Fatal("idempotent init or inspect wrote files")
	}
	config, err := os.ReadFile(filepath.Join(l.WorkflowRoot, ".git", "config"))
	if err != nil || strings.Contains(string(config), "[user]") || strings.Contains(string(config), "hooksPath") {
		t.Fatalf("unexpected Git config: %s, %v", config, err)
	}
}

func TestHistoryInitRefusesAdoptionAndRebind(t *testing.T) {
	for _, kind := range []string{"populated", "unowned git", "rebind", "nested git", "source overlap", "config overlap", "worktree overlap"} {
		t.Run(kind, func(t *testing.T) {
			l := historyTestLayout(t)
			switch kind {
			case "populated":
				historyWrite(t, filepath.Join(l.WorkflowRoot, "notes.md"), "do not adopt")
			case "unowned git":
				historyTestGit(t, l.ConfigRoot, "init", l.WorkflowRoot)
			case "rebind":
				if err := InitManaged(l); err != nil {
					t.Fatal(err)
				}
				l.ConfigPath = filepath.Join(l.ConfigRoot, "different.toml")
			case "nested git":
				historyTestGit(t, l.ConfigRoot, "init")
			case "source overlap":
				l.Repos["source"] = l.WorkflowRoot
			case "config overlap":
				l.WorkflowRoot = l.ConfigRoot
			case "worktree overlap":
				l.WorktreesDir = filepath.Join(l.WorkflowRoot, "execution")
			}
			before := historyDisk(t, l.ConfigRoot)
			if err := InitManaged(l); err == nil {
				t.Fatal("unsafe init succeeded")
			}
			if !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
				t.Fatal("refused init wrote files")
			}
		})
	}
}

func TestHistoryMissingIdentityPreflight(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		t.Run(fmt.Sprint(initialized), func(t *testing.T) {
			l := historyTestLayout(t)
			if initialized {
				if err := InitManaged(l); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			before := historyDisk(t, l.ConfigRoot)
			var err error
			called := false
			if initialized {
				err = RunMutation(l.ConfigRoot, "blocked", func() error { called = true; return nil })
			} else {
				err = InitManaged(l)
			}
			if err == nil || !strings.Contains(err.Error(), "identity") || called {
				t.Fatalf("missing identity = %v, callback = %t", err, called)
			}
			if !initialized && !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
				t.Fatal("missing identity init wrote files")
			}
		})
	}
}

func TestHistoryExistingModeAndLegacyPassThrough(t *testing.T) {
	l := historyTestLayout(t)
	historyWrite(t, l.ConfigPath, "schema_version = 2\n[workflow]\nroot = 'records'\ngit = 'existing'\n")
	l.GitMode = "existing"
	if err := InitManaged(l); err == nil {
		t.Fatal("existing mode accepted no Git owner")
	}
	historyTestGit(t, l.ConfigRoot, "init")
	before := historyDisk(t, l.ConfigRoot)
	if err := InitManaged(l); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectHistory(l); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
		t.Fatal("existing init/inspect had effects")
	}
	opErr := errors.New("operation failed")
	for _, config := range []string{"schema_version = 2\n[workflow]\ngit = 'existing'\n", "", "missing"} {
		if config == "missing" {
			if err := os.Remove(l.ConfigPath); err != nil {
				t.Fatal(err)
			}
		} else {
			historyWrite(t, l.ConfigPath, config)
		}
		called := false
		err := RunMutation(l.ConfigRoot, "legacy", func() error { called = true; return opErr })
		if !called || !errors.Is(err, opErr) {
			t.Fatalf("pass through %q: called=%t, err=%v", config, called, err)
		}
	}
}

func TestHistoryMutationScopesAndPartialProgress(t *testing.T) {
	l := historyInitTest(t)
	historyWrite(t, filepath.Join(l.WorkflowRoot, "guides", "preexisting.md"), "before")
	if err := Checkpoint(l, nil, "seed"); err != nil {
		t.Fatal(err)
	}
	historyWrite(t, filepath.Join(l.WorkflowRoot, "guides", "preexisting.md"), "unrelated edit")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "unrelated.md"), "unrelated new record")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package unrelated")
	configBefore, _ := os.ReadFile(l.ConfigPath)
	opErr := errors.New("second hop failed")
	err := RunMutation(l.ConfigRoot, "partial progress", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "changes", "one", "state.json"), "completed first hop")
		historyWrite(t, filepath.Join(l.WorkflowRoot, "changes", ".onto.lock"), "lock")
		historyWrite(t, filepath.Join(l.WorkflowRoot, "changes", ".staging", "record.md"), "staging")
		historyWrite(t, filepath.Join(l.WorkflowRoot, "changes", "one", ".homonto-atomic-tmp"), "incomplete atomic write")
		return opErr
	}, MutationScope{Trees: []string{"changes/one"}})
	if !errors.Is(err, opErr) || strings.Contains(err.Error(), "pending") {
		t.Fatalf("partial mutation error = %v", err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != "changes/one/state.json" {
		t.Fatalf("partial checkpoint consumed unrelated paths: %q", got)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "show", "HEAD:guides/preexisting.md"); got != "before" {
		t.Fatalf("preexisting edit consumed: %q", got)
	}
	configAfter, _ := os.ReadFile(l.ConfigPath)
	if string(configBefore) != string(configAfter) {
		t.Fatal("source configuration modified")
	}
}

func TestHistoryMutationExcludesConcurrentNeighborEdits(t *testing.T) {
	l := historyInitTest(t)
	scope := MutationScope{Paths: []string{"changes/one/onto-state.yaml"}}
	called := false
	if err := RunMutation(l.ConfigRoot, "unscoped", func() error { called = true; return nil }); err == nil || called {
		t.Fatalf("unscoped mutation ran: %v", err)
	}
	err := RunMutation(l.ConfigRoot, "state only", func() error {
		j, err := readHistoryJournal(l)
		if err != nil || j.Version != 3 || !reflect.DeepEqual(*j.Scope, scope) {
			t.Fatalf("intent not durable before callback: %+v, %v", j, err)
		}
		// A cooperating same-file writer is rejected before its callback, not
		// merely observed after two last-writer-wins state saves.
		if err := RunMutation(l.ConfigRoot, "competitor", func() error { called = true; return nil }, scope); err == nil || called {
			t.Fatalf("concurrent writer ran: %v", err)
		}
		historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/onto-state.yaml"), "owned")
		for _, p := range []string{"changes/one/tasks.md", "changes/other/onto-state.yaml", "specs/unrelated.md", "guides/manual.md"} {
			historyWrite(t, filepath.Join(l.WorkflowRoot, p), "concurrent editor")
		}
		return nil
	}, scope)
	if err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != scope.Paths[0] {
		t.Fatalf("captured neighboring edits: %q", got)
	}
}

func TestHistoryDetectsSameFileEditDuringNormalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test clean filter requires a POSIX shell")
	}
	l := historyInitTest(t)
	historyWrite(t, filepath.Join(l.WorkflowRoot, ".gitattributes"), "tasks/one.md filter=concurrent\n")
	historyTestConfig(t, "filter.concurrent.clean", "cat; printf editor > tasks/one.md", "filter.concurrent.required", "true")
	head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	err := RunMutation(l.ConfigRoot, "write state", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks/one.md"), "operation")
		return nil
	}, MutationScope{Paths: []string{"tasks/one.md"}})
	if err == nil || !strings.Contains(err.Error(), "changed since checkpoint") || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("same-file race not detected: %v", err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
		t.Fatal("concurrent content silently committed")
	}
	if view, err := InspectHistory(l); err != nil || !view.Pending {
		t.Fatalf("same-file race appeared normal: %+v, %v", view, err)
	}
}

func TestCommandMutationScopeSelectsOnlyCurrentOperation(t *testing.T) {
	l := historyInitTest(t)
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/specs/selected.MD"), "delta")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/specs/README.md"), "not a delta")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/other/specs/unrelated.md"), "unrelated delta")
	cmd := &cobra.Command{}
	s, err := commandMutationScope(l, cmd, "onto", "merge-deltas", []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	want := MutationScope{Paths: []string{"changes/one/onto-state.yaml", "changes/one/.onto/merge-receipt.json", "specs/selected.md"}}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("spec write set = %+v, want %+v", s, want)
	}
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/onto-state.yaml"), "change: one\n")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/archive/2026-01-01-one/onto-state.yaml"), "change: one\n")
	s, err = commandMutationScope(l, cmd, "onto", "close", []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	if s.contains("changes/archive/2026-01-01-one/onto-state.yaml") {
		t.Fatal("closing active generation owns previous archive")
	}
	for _, tree := range []string{"changes", "tasks/archive", "changes/archive", "specs"} {
		if err := (MutationScope{Trees: []string{tree}}).validate(); err == nil {
			t.Fatalf("accepted whole namespace %q", tree)
		}
	}
}

func TestHistoryCrashProcess(t *testing.T) {
	root := os.Getenv("HOMONTO_HISTORY_CRASH_ROOT")
	if root == "" {
		return
	}
	l, err := LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	err = RunMutation(root, "crash operation", func() error {
		j, err := readHistoryJournal(l)
		if err != nil || j.Version != 3 {
			t.Fatalf("missing pre-write intent: %v", err)
		}
		if os.Getenv("HOMONTO_HISTORY_CRASH_AFTER") == "1" {
			historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/state.json"), "first durable hop")
			if err := os.Remove(filepath.Join(l.WorkflowRoot, "changes/one/deleted.md")); err != nil {
				t.Fatal(err)
			}
			historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/unrelated/edit.md"), "not owned")
		}
		os.Exit(23) // no deferred cleanup, exactly the pre-Git crash window
		return nil
	}, MutationScope{Paths: []string{"changes/one/state.json", "changes/one/deleted.md"}})
	t.Fatalf("crash callback not reached: %v", err)
}

func TestHistoryPreparedArchiveCrash(t *testing.T) {
	if root := os.Getenv("HOMONTO_ARCHIVE_CRASH_ROOT"); root != "" {
		l, err := LoadRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		archiveNow = func() time.Time { return time.Date(2040, 1, 1, 23, 59, 59, 0, time.UTC) }
		cmd := &cobra.Command{Use: "to"}
		leaf := &cobra.Command{Use: "done", RunE: func(cmd *cobra.Command, _ []string) error {
			archiveNow = func() time.Time { return time.Date(2040, 1, 2, 0, 0, 1, 0, time.UTC) }
			source := filepath.Join(l.WorkflowRoot, "tasks/one")
			dest, date, planned, err := ArchiveTarget(cmd.Context(), source)
			if err != nil || !planned || date != "2040-01-01" {
				t.Fatalf("midnight changed intent: %s, %v", date, err)
			}
			j, err := readHistoryJournal(l)
			if err != nil || j.Scope.Archive == nil || filepath.Join(l.WorkflowRoot, j.Scope.Archive.Destination) != dest {
				t.Fatalf("actual target not persisted: %+v, %v", j, err)
			}
			historyWrite(t, filepath.Join(source, "to-state.yaml"), "change: one\nphase: done\nfinished: "+date+"\n")
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := CheckArchiveTarget(cmd.Context(), source, dest); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(source, dest); err != nil {
				t.Fatal(err)
			}
			historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks/archive/unrelated/record.md"), "concurrent record")
			os.Exit(23)
			return nil
		}}
		leaf.Flags().String("dir", root, "")
		cmd.AddCommand(leaf)
		AttachHistory(cmd, "to")
		cmd.SetArgs([]string{"done", "one"})
		t.Fatalf("crash callback not reached: %v", cmd.Execute())
	}
	l := historyInitTest(t)
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks/one/to-state.yaml"), "change: one\nphase: do\n")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks/one/plan.md"), "plan")
	if err := Checkpoint(l, nil, "seed"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHistoryPreparedArchiveCrash$")
	cmd.Env = append(os.Environ(), "HOMONTO_ARCHIVE_CRASH_ROOT="+l.ConfigRoot)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("child: %s, %v", out, err)
	}
	if err := RecoverHistory(l); err != nil {
		t.Fatal(err)
	}
	paths := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "--no-renames", "HEAD")
	if !strings.Contains(paths, "tasks/archive/2040-01-01-one/to-state.yaml") || !strings.Contains(paths, "tasks/one/to-state.yaml") || strings.Contains(paths, "unrelated") {
		t.Fatalf("recovery paths: %s", paths)
	}
	if subject := historyTestGit(t, l.WorkflowRoot, "log", "-1", "--format=%s"); subject != "Recover interrupted operation: to done" {
		t.Fatalf("false normal archive: %s", subject)
	}
}

func TestPreparedArchiveRejectsReplacedSource(t *testing.T) {
	l := historyInitTest(t)
	source := filepath.Join(l.WorkflowRoot, "tasks/one")
	historyWrite(t, filepath.Join(source, "to-state.yaml"), "change: one\nphase: do\n")
	if err := Checkpoint(l, nil, "seed"); err != nil {
		t.Fatal(err)
	}
	head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	cmd := &cobra.Command{}
	err := runMutation(l.ConfigRoot, "to done", func(l Layout) (MutationScope, error) {
		return commandMutationScope(l, cmd, "to", "done", []string{"one"})
	}, func() error {
		if err := os.Rename(source, filepath.Join(l.ConfigRoot, "original")); err != nil {
			return err
		}
		historyWrite(t, filepath.Join(source, "to-state.yaml"), "change: one\nphase: do\n")
		_, _, _, err := ArchiveTarget(cmd.Context(), source)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "source directory replaced") {
		t.Fatalf("replacement accepted: %v", err)
	}
	if historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD") != head {
		t.Fatal("replaced source falsely checkpointed")
	}
	if view, err := InspectHistory(l); err != nil || !view.Pending {
		t.Fatalf("replacement looks healthy: %+v, %v", view, err)
	}
}

func TestHistoryRecoversPreWriteIntentAfterProcessExit(t *testing.T) {
	for _, after := range []string{"0", "1"} {
		t.Run(after, func(t *testing.T) {
			l := historyInitTest(t)
			historyWrite(t, filepath.Join(l.WorkflowRoot, "changes/one/deleted.md"), "seed")
			if err := Checkpoint(l, nil, "seed"); err != nil {
				t.Fatal(err)
			}
			head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
			cmd := exec.Command(os.Args[0], "-test.run=^TestHistoryCrashProcess$")
			cmd.Env = append(os.Environ(), "HOMONTO_HISTORY_CRASH_ROOT="+l.ConfigRoot, "HOMONTO_HISTORY_CRASH_AFTER="+after)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("child: %s: %v", out, err)
			}
			if view, err := InspectHistory(l); err != nil || !view.Pending {
				t.Fatalf("crash looked healthy: %+v, %v", view, err)
			}
			if err := RecoverHistory(l); err != nil {
				t.Fatal(err)
			}
			if after == "0" {
				if got := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
					t.Fatal("pre-write crash created a commit")
				}
			} else {
				if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != "changes/one/deleted.md\nchanges/one/state.json" {
					t.Fatalf("recovery write set: %q", got)
				}
				if got := historyTestGit(t, l.WorkflowRoot, "log", "-1", "--format=%s"); got != "Recover interrupted operation: crash operation" {
					t.Fatalf("recovery claimed normal completion: %q", got)
				}
			}
			if err := noPendingHistory(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryCheckpointSelectionLiteralPathsAndDeletes(t *testing.T) {
	l := historyInitTest(t)
	literal := "changes/one/[literal]*.md"
	historyWrite(t, filepath.Join(l.WorkflowRoot, literal), "selected")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "changes", "one", "l.md"), "not selected")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "guides", "manual.md"), "manual")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package source")
	if err := Checkpoint(l, []string{literal}, "literal selection"); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != literal {
		t.Fatalf("literal selection changed %q", got)
	}
	if err := Checkpoint(l, nil, "owned default"); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "ls-files"); strings.Contains(got, "source.go") {
		t.Fatalf("default included source: %q", got)
	}
	for _, path := range []string{".", "../homonto.toml", "source.go", "changes/../source.go", "changes/.onto.lock", "changes/.git/config"} {
		if err := Checkpoint(l, []string{path}, "unsafe"); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	err := RunMutation(l.ConfigRoot, "archive", func() error {
		if err := os.MkdirAll(filepath.Join(l.WorkflowRoot, "changes", "archive"), 0o755); err != nil {
			return err
		}
		return os.Rename(filepath.Join(l.WorkflowRoot, "changes", "one"), filepath.Join(l.WorkflowRoot, "changes", "archive", "one"))
	}, MutationScope{Trees: []string{"changes/one", "changes/archive/one"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "ls-files"); strings.Contains(got, "\nchanges/one/") || !strings.Contains(got, "changes/archive/one/") {
		t.Fatalf("archive did not commit moves/deletions: %q", got)
	}
	if err := os.Remove(filepath.Join(l.WorkflowRoot, "guides", "manual.md")); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(l, []string{"guides/manual.md"}, "remove manual"); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryNoEmptyCommit(t *testing.T) {
	l := historyInitTest(t)
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "committed")
	if err := Checkpoint(l, nil, "seed"); err != nil {
		t.Fatal(err)
	}
	head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	if err := Checkpoint(l, nil, "unchanged"); err != nil {
		t.Fatal(err)
	}
	if err := RunMutation(l.ConfigRoot, "unchanged", func() error { return nil }, MutationScope{}); err != nil {
		t.Fatal(err)
	}
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "dirty")
	if err := RunMutation(l.ConfigRoot, "restore", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "committed")
		return nil
	}, MutationScope{Paths: []string{"tasks/record.md"}}); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
		t.Fatal("unchanged operation created a commit")
	}
	if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "no pending") {
		t.Fatalf("recovery without pending = %v", err)
	}
}

func historyTestConfig(t *testing.T, pairs ...string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", fmt.Sprint(len(pairs)/2))
	for i := 0; i < len(pairs); i += 2 {
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i/2), pairs[i])
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i/2), pairs[i+1])
	}
}

func TestHistoryNormalization(t *testing.T) {
	for _, kind := range []string{"autocrlf", "attributes", "clean filter"} {
		t.Run(kind, func(t *testing.T) {
			l := historyInitTest(t)
			raw, normalized := "# Heading\r\nbody\r\n", "# Heading\nbody\n"
			switch kind {
			case "autocrlf":
				historyTestConfig(t, "core.autocrlf", "true")
			case "attributes":
				historyTestConfig(t, "core.autocrlf", "false")
				historyWrite(t, filepath.Join(l.WorkflowRoot, ".gitattributes"), "tasks/*.md text eol=lf\n")
			case "clean filter":
				if runtime.GOOS == "windows" {
					t.Skip("test clean filter requires a POSIX shell and tr")
				}
				historyTestConfig(t, "filter.history.clean", "case %f in tasks/*.md) tr a-z A-Z ;; *) exit 1 ;; esac", "filter.history.required", "true")
				historyWrite(t, filepath.Join(l.WorkflowRoot, ".gitattributes"), "tasks/*.md filter=history\n")
				raw, normalized = "lower\n", "LOWER\n"
			}
			historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package unrelated")
			for _, operation := range []string{"checkpoint", "mutation"} {
				path := "tasks/" + operation + ".md"
				var err error
				if operation == "checkpoint" {
					historyWrite(t, filepath.Join(l.WorkflowRoot, path), raw)
					err = Checkpoint(l, []string{path}, "normalized checkpoint")
				} else {
					err = RunMutation(l.ConfigRoot, "normalized mutation", func() error {
						historyWrite(t, filepath.Join(l.WorkflowRoot, path), raw)
						return nil
					}, MutationScope{Paths: []string{path}})
				}
				if err != nil {
					t.Fatalf("%s: %v", operation, err)
				}
				blob, err := historyGit(l, "show", "HEAD:"+path)
				if err != nil || string(blob) != normalized {
					t.Fatalf("normalized blob = %q, %v; want %q", blob, err, normalized)
				}
				data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, path))
				if err != nil || string(data) != raw {
					t.Fatalf("working bytes changed: %q, %v", data, err)
				}
				if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != path {
					t.Fatalf("checkpoint consumed extra paths: %q", got)
				}
			}
			head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
			if err := Checkpoint(l, nil, "normalized no-op"); err != nil {
				t.Fatal(err)
			}
			if err := RunMutation(l.ConfigRoot, "normalization-only edit", func() error {
				historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "mutation.md"), normalized)
				return nil
			}, MutationScope{Paths: []string{"tasks/mutation.md"}}); err != nil {
				t.Fatal(err)
			}
			if got := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
				t.Fatal("normalization-only edit created a commit")
			}
		})
	}
}

func TestHistoryNormalizedRecovery(t *testing.T) {
	for _, kind := range []string{"autocrlf", "clean filter"} {
		t.Run(kind, func(t *testing.T) {
			l := historyInitTest(t)
			raw, equivalent := "saved\r\n", "saved\n"
			historyTestConfig(t, "core.autocrlf", "true")
			if kind == "clean filter" {
				if runtime.GOOS == "windows" {
					t.Skip("test clean filter requires a POSIX shell and tr")
				}
				historyTestConfig(t, "filter.history.clean", "tr a-z A-Z", "filter.history.required", "true")
				historyWrite(t, filepath.Join(l.WorkflowRoot, ".gitattributes"), "tasks/*.md filter=history\n")
				raw, equivalent = "saved\n", "SAVED\n"
			}
			hook := historyFailHook(t, l)
			path := filepath.Join(l.WorkflowRoot, "tasks", "record.md")
			err := RunMutation(l.ConfigRoot, "normalized pending", func() error {
				historyWrite(t, path, raw)
				return nil
			}, MutationScope{Paths: []string{"tasks/record.md"}})
			if err == nil || !strings.Contains(err.Error(), "git commit:") {
				t.Fatalf("expected hook failure after successful staging, got %v", err)
			}
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			j, err := readHistoryJournal(l)
			if err != nil || j.Version != 2 || j.Paths["tasks/record.md"] != historyHash("100644", []byte(raw)) || j.GitPaths["tasks/record.md"] != "100644:"+historyTestGit(t, l.WorkflowRoot, "rev-parse", ":tasks/record.md") {
				t.Fatalf("separate pending fingerprints = %+v, %v", j, err)
			}
			index, err := os.ReadFile(filepath.Join(l.WorkflowRoot, ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "autocrlf" {
				historyTestConfig(t, "core.autocrlf", "false")
			} else {
				historyTestConfig(t, "filter.history.clean", "cat", "filter.history.required", "true")
			}
			if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "normalization changed") {
				t.Fatalf("recovery accepted changed normalization: %v", err)
			}
			after, err := os.ReadFile(filepath.Join(l.WorkflowRoot, ".git", "index"))
			if err != nil || string(index) != string(after) {
				t.Fatalf("normalization preflight changed the index: %v", err)
			}
			if kind == "autocrlf" {
				historyTestConfig(t, "core.autocrlf", "true")
			} else {
				historyTestConfig(t, "filter.history.clean", "tr a-z A-Z", "filter.history.required", "true")
			}
			for _, edit := range []string{equivalent, "new content\n"} {
				historyWrite(t, path, edit)
				if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "changed since") {
					t.Fatalf("recovery accepted raw edit %q: %v", edit, err)
				}
			}
			historyWrite(t, path, "unexpected staged content\n")
			historyTestGit(t, l.WorkflowRoot, "add", "tasks/record.md")
			historyWrite(t, path, raw)
			if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "pending index differs") {
				t.Fatalf("recovery accepted unexpected normalized index content: %v", err)
			}
			historyTestGit(t, l.WorkflowRoot, "add", "tasks/record.md")
			historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package unrelated")
			historyTestGit(t, l.WorkflowRoot, "add", "source.go")
			if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "staged path") {
				t.Fatalf("recovery accepted an extra index path: %v", err)
			}
			historyTestGit(t, l.WorkflowRoot, "restore", "--staged", "source.go")
			if err := RecoverHistory(l); err != nil {
				t.Fatal(err)
			}
			if err := noPendingHistory(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryFilterFailureBeforeStaging(t *testing.T) {
	l := historyInitTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("test clean filter requires a POSIX shell")
	}
	historyTestConfig(t, "filter.history.clean", "exit 1", "filter.history.required", "true")
	historyWrite(t, filepath.Join(l.WorkflowRoot, ".gitattributes"), "tasks/bad.md filter=history\n")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "good.md"), "good\n")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "bad.md"), "bad\n")
	head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
	if err := Checkpoint(l, nil, "filter failure"); err == nil || !strings.Contains(err.Error(), "normaliz") || !strings.Contains(err.Error(), "tasks/bad.md") {
		t.Fatalf("filter preflight error = %v", err)
	}
	if err := noPendingHistory(l); err != nil {
		t.Fatalf("filter preflight left a pending journal: %v", err)
	}
	if err := historyIndex(l, nil); err != nil {
		t.Fatalf("filter preflight staged junk: %v", err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD"); got != head {
		t.Fatal("filter preflight committed")
	}
	opErr := errors.New("partial mutation")
	err := RunMutation(l.ConfigRoot, "filter failure", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "bad.md"), "mutated\n")
		return opErr
	}, MutationScope{Paths: []string{"tasks/bad.md"}})
	if !errors.Is(err, opErr) || !strings.Contains(err.Error(), "normalization failed") {
		t.Fatalf("filter and operation errors were not joined: %v", err)
	}
	if j, err := readHistoryJournal(l); err != nil || j.Version != 3 {
		t.Fatalf("normalization failure lost mutation intent: %+v, %v", j, err)
	}
	if err := historyIndex(l, nil); err != nil {
		t.Fatal(err)
	}
	historyTestConfig(t, "filter.history.clean", "cat", "filter.history.required", "true")
	if err := RecoverHistory(l); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(l, nil, "repaired filter"); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryNormalizedModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable-bit test requires POSIX file modes")
	}
	for _, filemode := range []string{"true", "false"} {
		t.Run(filemode, func(t *testing.T) {
			l := historyInitTest(t)
			historyTestConfig(t, "core.autocrlf", "true", "core.filemode", filemode)
			hook := historyFailHook(t, l)
			path := filepath.Join(l.WorkflowRoot, "tasks", "executable.md")
			historyWrite(t, path, "record\r\n")
			if err := os.Chmod(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := Checkpoint(l, nil, "executable record"); err == nil || !strings.Contains(err.Error(), "git commit:") {
				t.Fatalf("expected commit hook failure, got %v", err)
			}
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "changed since") {
				t.Fatalf("recovery ignored raw mode change: %v", err)
			}
			if err := os.Chmod(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := RecoverHistory(l); err != nil {
				t.Fatal(err)
			}
			mode := "100755"
			if filemode == "false" {
				mode = "100644"
			}
			if got := historyTestGit(t, l.WorkflowRoot, "ls-tree", "HEAD", "tasks/executable.md"); !strings.HasPrefix(got, mode+" blob ") {
				t.Fatalf("committed Git mode = %q", got)
			}
		})
	}
}

func TestHistoryNormalizedJournalValidation(t *testing.T) {
	l := historyInitTest(t)
	historyTestConfig(t, "core.autocrlf", "true")
	hook := historyFailHook(t, l)
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "record\r\n")
	if err := Checkpoint(l, nil, "pending"); err == nil {
		t.Fatal("hook did not fail")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(journalPath(l))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"old version", "missing Git path", "extra Git path", "inconsistent deletion", "invalid object"} {
		t.Run(kind, func(t *testing.T) {
			var j historyJournal
			if err := json.Unmarshal(saved, &j); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "old version":
				j.Version = 1
			case "missing Git path":
				delete(j.GitPaths, "tasks/record.md")
			case "extra Git path":
				j.GitPaths["source.go"] = j.GitPaths["tasks/record.md"]
			case "inconsistent deletion":
				j.GitPaths["tasks/record.md"] = ""
			case "invalid object":
				j.GitPaths["tasks/record.md"] = "100644:not-an-object-id"
			}
			data, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			historyWrite(t, journalPath(l), string(data))
			if err := RecoverHistory(l); err == nil {
				t.Fatal("recovery accepted an invalid journal")
			}
		})
	}
	historyWrite(t, journalPath(l), string(saved))
	if err := RecoverHistory(l); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryNormalizationRejectsHookWrites(t *testing.T) {
	for _, kind := range []string{"equivalent raw edit", "extra path"} {
		t.Run(kind, func(t *testing.T) {
			l := historyInitTest(t)
			historyTestConfig(t, "core.autocrlf", "true")
			hook := historyFailHook(t, l)
			path := filepath.Join(l.WorkflowRoot, "tasks", "record.md")
			historyWrite(t, path, "record\r\n")
			historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package unrelated")
			script := "#!/bin/sh\nprintf 'record\\n' > tasks/record.md\n"
			if kind == "extra path" {
				script = "#!/bin/sh\ngit add source.go\n"
			}
			historyWrite(t, hook, script)
			err := Checkpoint(l, nil, "hook changes")
			if err == nil || !strings.Contains(err.Error(), "checkpoint pending") {
				t.Fatalf("hook writes reported success: %v", err)
			}
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			historyWrite(t, path, "record\r\n")
			err = RecoverHistory(l)
			if kind == "extra path" {
				if err == nil || !strings.Contains(err.Error(), "committed paths or content differ") {
					t.Fatalf("recovery accepted an extra committed path: %v", err)
				}
			} else if err != nil {
				t.Fatalf("exact commit could not recover after restoring raw bytes: %v", err)
			}
		})
	}
}

func historyFailHook(t *testing.T, l Layout) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test hook requires a POSIX shell")
	}
	path := filepath.Join(l.WorkflowRoot, ".git", "hooks", "pre-commit")
	historyWrite(t, path, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoryHookFailureAndRecovery(t *testing.T) {
	l := historyInitTest(t)
	hook := historyFailHook(t, l)
	opErr := errors.New("partial operation failed")
	err := RunMutation(l.ConfigRoot, "partial pending", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "one.md"), "saved progress")
		return opErr
	}, MutationScope{Paths: []string{"tasks/one.md"}})
	if !errors.Is(err, opErr) || !strings.Contains(err.Error(), "homonto workspace recover") {
		t.Fatalf("joined operation/history error = %v", err)
	}
	j, err := readHistoryJournal(l)
	if err != nil || len(j.Paths) != 1 || j.Paths["tasks/one.md"] == "" {
		t.Fatalf("pending journal = %+v, %v", j, err)
	}
	called := false
	if err := RunMutation(l.ConfigRoot, "blocked", func() error { called = true; return nil }); err == nil || called {
		t.Fatalf("pending mutation: called=%t, err=%v", called, err)
	}
	before := historyDisk(t, l.ConfigRoot)
	view, err := InspectHistory(l)
	if err != nil || !view.Pending || !reflect.DeepEqual(before, historyDisk(t, l.ConfigRoot)) {
		t.Fatalf("pending inspect = %+v, %v, or inspection wrote", view, err)
	}
	if err := RecoverHistory(l); err == nil {
		t.Fatal("failing hook reported recovery success")
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "one.md"), "later edit")
	if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("edited pending record recovery = %v", err)
	}
	historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "one.md"), "saved progress")
	historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package source")
	historyTestGit(t, l.WorkflowRoot, "add", "source.go")
	if err := RecoverHistory(l); err == nil || !strings.Contains(err.Error(), "staged path") {
		t.Fatalf("unrelated index recovery = %v", err)
	}
	historyTestGit(t, l.WorkflowRoot, "restore", "--staged", "source.go")
	if err := RecoverHistory(l); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "show", "--format=", "--name-only", "HEAD"); got != "tasks/one.md" {
		t.Fatalf("recovery consumed unrelated paths: %q", got)
	}
	if err := noPendingHistory(l); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryInitHookFailureRecoverable(t *testing.T) {
	l := historyTestLayout(t)
	if runtime.GOOS == "windows" {
		t.Skip("test hook requires a POSIX shell")
	}
	template := t.TempDir()
	hook := filepath.Join(template, "hooks", "pre-commit")
	historyWrite(t, hook, "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", template)
	if err := InitManaged(l); err == nil || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("failed initial commit = %v", err)
	}
	if err := InitManaged(l); err == nil || !strings.Contains(err.Error(), "recover") {
		t.Fatalf("init with pending initialization = %v", err)
	}
	if err := os.Remove(filepath.Join(l.WorkflowRoot, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}
	if err := RecoverHistory(l); err != nil {
		t.Fatal(err)
	}
	if got := historyTestGit(t, l.WorkflowRoot, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("initial recovery commit count = %s", got)
	}
}

func TestHistoryRecoverDeletionAndInterruptedCleanup(t *testing.T) {
	for _, kind := range []string{"deletion", "committed before cleanup", "moved head", "changed index"} {
		t.Run(kind, func(t *testing.T) {
			l := historyInitTest(t)
			path := filepath.Join(l.WorkflowRoot, "guides", "record.md")
			historyWrite(t, path, "original")
			if err := Checkpoint(l, nil, "seed"); err != nil {
				t.Fatal(err)
			}
			hook := historyFailHook(t, l)
			if kind == "deletion" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				historyWrite(t, path, "pending")
			}
			if err := Checkpoint(l, nil, "pending"); err == nil {
				t.Fatal("hook did not fail")
			}
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "committed before cleanup":
				historyTestGit(t, l.WorkflowRoot, "commit", "-m", "pending")
			case "moved head":
				historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package source")
				historyTestGit(t, l.WorkflowRoot, "add", "source.go")
				historyTestGit(t, l.WorkflowRoot, "commit", "-m", "unexpected other commit")
			case "changed index":
				historyWrite(t, path, "staged later")
				historyTestGit(t, l.WorkflowRoot, "add", "guides/record.md")
				historyWrite(t, path, "pending")
			}
			head := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD")
			err := RecoverHistory(l)
			if kind == "moved head" || kind == "changed index" {
				if err == nil {
					t.Fatal("recovery accepted unrelated HEAD/index changes")
				}
				if err := noPendingHistory(l); err == nil {
					t.Fatal("failed recovery discarded pending journal")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "committed before cleanup" && historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD") != head {
				t.Fatal("cleanup recovery created another commit")
			}
			if err := noPendingHistory(l); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoryStandaloneExternalRoot(t *testing.T) {
	l := historyTestLayout(t)
	external := t.TempDir()
	historyWrite(t, l.ConfigPath, fmt.Sprintf("schema_version = 2\n[workflow]\nroot = %q\ngit = 'managed'\n", filepath.ToSlash(external)))
	var err error
	l, err = LoadRoot(l.ConfigRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitManaged(l); err != nil {
		t.Fatal(err)
	}
	if err := RunMutation(l.ConfigRoot, "external records", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "external")
		return nil
	}, MutationScope{Paths: []string{"tasks/record.md"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRoot(l.ConfigRoot); err != nil {
		t.Fatalf("initialized external layout cannot reload: %v", err)
	}
}

func TestHistoryGitEnvironmentCannotRedirectSourceWrites(t *testing.T) {
	l := historyInitTest(t)
	source := t.TempDir()
	historyTestGit(t, source, "init")
	historyWrite(t, filepath.Join(source, "source.go"), "package source")
	historyTestGit(t, source, "add", "source.go")
	historyTestGit(t, source, "commit", "-m", "source seed")
	before := historyDisk(t, source)
	t.Setenv("GIT_DIR", filepath.Join(source, ".git"))
	t.Setenv("GIT_WORK_TREE", source)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(source, ".git", "index"))
	err := RunMutation(l.ConfigRoot, "workflow only", func() error {
		historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "record.md"), "record")
		return nil
	}, MutationScope{Paths: []string{"tasks/record.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, historyDisk(t, source)) {
		t.Fatal("history wrote to the source repository through inherited Git environment")
	}
}

func TestHistoryIndexAndSharedLockBlockBeforeMutation(t *testing.T) {
	for _, kind := range []string{"staged", "conflict", "lock", "uninitialized"} {
		t.Run(kind, func(t *testing.T) {
			l := historyTestLayout(t)
			if kind != "uninitialized" {
				if err := InitManaged(l); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "staged":
				historyWrite(t, filepath.Join(l.WorkflowRoot, "source.go"), "package source")
				historyTestGit(t, l.WorkflowRoot, "add", "source.go")
			case "conflict":
				blob := historyTestGit(t, l.WorkflowRoot, "rev-parse", "HEAD:"+ownershipFile)
				cmd := exec.Command("git", "-C", l.WorkflowRoot, "update-index", "--index-info")
				cmd.Stdin = strings.NewReader("100644 " + blob + " 1\ttasks/conflict.md\n100644 " + blob + " 2\ttasks/conflict.md\n")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("create conflict: %s, %v", out, err)
				}
			case "lock":
				lock, err := applylock.AcquireProcess(filepath.Join(l.WorkflowRoot, ".git", "homonto-history"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Release()
			}
			called := false
			err := RunMutation(l.ConfigRoot, "blocked", func() error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("unsafe callback ran: called=%t, err=%v", called, err)
			}
		})
	}
}

func TestHistoryRejectsSymlinksAndNestedGit(t *testing.T) {
	for _, kind := range []string{"file symlink", "directory symlink", "nested git", "journal symlink"} {
		t.Run(kind, func(t *testing.T) {
			l := historyInitTest(t)
			outside := t.TempDir()
			historyWrite(t, filepath.Join(outside, "record.md"), "outside")
			historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "safe.md"), "safe")
			switch kind {
			case "file symlink", "directory symlink":
				target := outside
				if kind == "file symlink" {
					target = filepath.Join(outside, "record.md")
				}
				if err := os.Symlink(target, filepath.Join(l.WorkflowRoot, "tasks", "escape")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			case "nested git":
				historyWrite(t, filepath.Join(l.WorkflowRoot, "tasks", "nested", ".git"), "gitdir: elsewhere")
			case "journal symlink":
				if err := os.Symlink(filepath.Join(outside, "record.md"), journalPath(l)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if err := Checkpoint(l, nil, "unsafe"); err == nil {
				t.Fatal("unsafe checkpoint succeeded")
			}
			if got := historyTestGit(t, l.WorkflowRoot, "rev-list", "--count", "HEAD"); got != "1" {
				t.Fatal("unsafe checkpoint committed")
			}
		})
	}
}
