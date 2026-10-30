package workflowcleanup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/cli"
	"github.com/noviopenworks/homonto/internal/ontocli"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tocli"
	"github.com/noviopenworks/homonto/internal/workflowcleanup"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func command(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("%s %v: %v\n%s", root.Name(), args, err, out.String())
	}
	return out.String()
}

func fixture(t *testing.T) workspace.Layout {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Cleanup Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "cleanup@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Cleanup Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "cleanup@example.invalid")
	root := t.TempDir()
	source := filepath.Join(root, "source")
	git(t, root, "init", "-b", "main", source)
	write(t, filepath.Join(source, "source.txt"), "base\n")
	git(t, source, "add", "source.txt")
	git(t, source, "commit", "-m", "base")
	write(t, filepath.Join(root, "homonto.toml"), "schema_version = 2\n[workflow]\nroot = 'records'\ngit = 'managed'\n[worktrees]\ndir = 'execution'\n[repos]\nsource = 'source'\n[engines.onto]\n[engines.to]\n")
	for _, engine := range []string{"onto", "to"} {
		if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", engine), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	command(t, cli.NewRootCmd(), "workspace", "init", "--yes", "--config", l.ConfigPath)
	return l
}

func native(engine string) *cobra.Command {
	if engine == "onto" {
		return ontocli.NewRootCmd()
	}
	return tocli.NewRootCmd()
}

func preview(t *testing.T, l workspace.Layout, engine, name, identity string) workflowcleanup.Plan {
	t.Helper()
	p, err := workflowcleanup.Preview(l, engine, name, identity)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCleanupAllBinaryEntrypointsAndReadOnlyInventory(t *testing.T) {
	l := fixture(t)
	command(t, native("onto"), "new", "feature", "--repo", "source", "--dir", l.ConfigRoot)
	head := git(t, l.WorkflowRoot, "rev-parse", "HEAD")
	for _, root := range []*cobra.Command{cli.NewRootCmd(), native("onto"), native("to")} {
		args := []string{"cleanup", "list", "--json", "--dir", l.ConfigRoot}
		if root.Name() == "homonto" {
			args = []string{"workflow", "cleanup", "list", "--json", "--config", l.ConfigPath}
		}
		var report workflowcleanup.Inventory
		if err := json.Unmarshal([]byte(command(t, root, args...)), &report); err != nil {
			t.Fatal(err)
		}
		if report.SchemaVersion != 1 || report.ConfigPath != l.ConfigPath || report.Items == nil {
			t.Fatalf("bad inventory: %+v", report)
		}
	}
	p := preview(t, l, "onto", "feature", "")
	if p.Status != "ineligible" || p.Identity == "" {
		t.Fatalf("active work was eligible: %+v", p)
	}
	if _, err := workflowcleanup.Apply(context.Background(), l, "onto", "feature", p.Identity, true); err == nil {
		t.Fatal("cleanup abandoned active work")
	}
	if git(t, l.WorkflowRoot, "rev-parse", "HEAD") != head || git(t, l.WorkflowRoot, "status", "--porcelain") != "" {
		t.Fatal("read-only inventory/refused apply changed records")
	}
	if _, err := os.Stat(filepath.Join(l.WorkflowRoot, ".workflow", "cleanup")); !os.IsNotExist(err) {
		t.Fatalf("inspection wrote cleanup receipts: %v", err)
	}
}

func TestCleanupPreservesTerminalHistoryAndReleasesNames(t *testing.T) {
	for _, engine := range []string{"onto", "to"} {
		t.Run(engine, func(t *testing.T) {
			l := fixture(t)
			command(t, native(engine), "new", "repeat", "--repo", "source", "--dir", l.ConfigRoot)
			w, err := workspace.CreateWorktree(l, engine, "repeat", "source", "main", "work/repeat")
			if err != nil {
				t.Fatal(err)
			}
			command(t, native(engine), "abandon", "repeat", "--dir", l.ConfigRoot)
			p := preview(t, l, engine, "repeat", "")
			if p.Status != "cleanup-pending" || len(p.Worktrees) != 1 || len(p.Blockers) != 0 {
				t.Fatalf("terminal cleanup not discovered: %+v", p)
			}
			stateBefore, err := os.ReadFile(filepath.Join(l.WorkflowRoot, p.Record, engine+"-state.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			for _, selection := range []struct {
				identity string
				yes      bool
			}{{p.Identity, false}, {"", true}, {"wrong-generation", true}} {
				if _, err := workflowcleanup.Apply(context.Background(), l, engine, "repeat", selection.identity, selection.yes); err == nil {
					t.Fatalf("unconfirmed cleanup accepted: %+v", selection)
				}
			}
			out := command(t, native(engine), "cleanup", "apply", "repeat", "--identity", p.Identity, "--yes", "--json", "--dir", l.ConfigRoot)
			var done workflowcleanup.Plan
			if err := json.Unmarshal([]byte(out), &done); err != nil || done.Status != "cleanup-complete" {
				t.Fatalf("cleanup failed: %v\n%s", err, out)
			}
			if !strings.Contains(done.Record, "/archive/") || done.Identity != p.Identity {
				t.Fatalf("terminal identity/history was lost: %+v", done)
			}
			stateAfter, err := os.ReadFile(filepath.Join(l.WorkflowRoot, done.Record, engine+"-state.yaml"))
			if err != nil || !bytes.Equal(stateBefore, stateAfter) {
				t.Fatalf("cleanup rewrote engine history: %v", err)
			}
			if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
				t.Fatalf("worktree not removed: %v", err)
			}
			git(t, l.Repos["source"], "rev-parse", "refs/heads/"+w.Branch) // retained, never force-deleted
			if dirt := git(t, l.WorkflowRoot, "status", "--porcelain"); dirt != "" {
				t.Fatalf("managed cleanup was not checkpointed: %s", dirt)
			}
			head := git(t, l.WorkflowRoot, "rev-parse", "HEAD")
			if _, err := workflowcleanup.Apply(context.Background(), l, engine, "repeat", p.Identity, true); err != nil {
				t.Fatal(err)
			}
			if git(t, l.WorkflowRoot, "rev-parse", "HEAD") != head {
				t.Fatal("idempotent cleanup created an empty checkpoint")
			}
			command(t, native(engine), "new", "repeat", "--repo", "source", "--dir", l.ConfigRoot)
			next, err := workspace.CreateWorktree(l, engine, "repeat", "source", "main", "work/next-repeat")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := workflowcleanup.Preview(l, engine, "repeat", ""); err == nil {
				t.Fatal("ambiguous generations were silently selected")
			}
			if old := preview(t, l, engine, "repeat", p.Identity); old.Status != "cleanup-complete" {
				t.Fatalf("name reuse changed old generation: %+v", old)
			}
			if _, err := workflowcleanup.Apply(context.Background(), l, engine, "repeat", p.Identity, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(next.Path); err != nil {
				t.Fatal("old generation cleanup removed the new active checkout")
			}
		})
	}
}

func TestCompletedCleanupAndIncompleteIntegration(t *testing.T) {
	t.Run("completed to", func(t *testing.T) {
		l := fixture(t)
		command(t, native("to"), "new", "complete", "--repo", "source", "--dir", l.ConfigRoot)
		if _, err := workspace.CreateWorktree(l, "to", "complete", "source", "main", "work/complete"); err != nil {
			t.Fatal(err)
		}
		command(t, native("to"), "phase", "complete", "--dir", l.ConfigRoot)
		command(t, native("to"), "done", "complete", "--verified", "--evidence", "fixture verification", "--dir", l.ConfigRoot)
		p := preview(t, l, "to", "complete", "")
		if p.Lifecycle != "completed" || p.Status != "cleanup-pending" {
			t.Fatalf("completed workflow not discoverable: %+v", p)
		}
		done, err := workflowcleanup.Apply(context.Background(), l, "to", "complete", p.Identity, true)
		if err != nil || done.Status != "cleanup-complete" || done.Record != p.Record {
			t.Fatalf("completed archive was not preserved: %+v, %v", done, err)
		}
	})
	for _, integrated := range []bool{false, true} {
		t.Run(map[bool]string{false: "onto integration pending", true: "onto completed archive"}[integrated], func(t *testing.T) {
			l := fixture(t)
			command(t, native("onto"), "new", "complete", "--repo", "source", "--dir", l.ConfigRoot)
			if _, err := workspace.CreateWorktree(l, "onto", "complete", "source", "main", "work/complete"); err != nil {
				t.Fatal(err)
			}
			// Seed an exact post-archive fixture. No verification or integration
			// is manufactured by cleanup: the required marker alone blocks it.
			source := filepath.Join(l.WorkflowRoot, "changes", "complete")
			st, err := ontostate.LoadChange(source)
			if err != nil {
				t.Fatal(err)
			}
			st.Phase, st.Archived, st.IntegrationRequired = "close", true, !integrated
			if err := ontostate.Save(filepath.Join(source, "onto-state.yaml"), st); err != nil {
				t.Fatal(err)
			}
			archive := "changes/archive/2026-10-27-complete"
			if err := os.MkdirAll(filepath.Dir(filepath.Join(l.WorkflowRoot, archive)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(source, filepath.Join(l.WorkflowRoot, archive)); err != nil {
				t.Fatal(err)
			}
			if err := workspace.Checkpoint(l, []string{"changes/complete", archive}, "Seed post-archive fixture"); err != nil {
				t.Fatal(err)
			}
			p := preview(t, l, "onto", "complete", "")
			got, err := workflowcleanup.Apply(context.Background(), l, "onto", "complete", p.Identity, true)
			if integrated {
				if err != nil || got.Status != "cleanup-complete" || got.Record != archive {
					t.Fatalf("completed cleanup failed: %+v, %v", got, err)
				}
			} else if err == nil || got.Status != "ineligible" {
				t.Fatalf("incomplete integration was waived: %+v, %v", got, err)
			}
		})
	}
}

func TestCleanupRefusesDirtyIgnoredAndChangedEvidence(t *testing.T) {
	for _, kind := range []string{"dirty", "ignored", "record-dirty", "record-hidden", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			l := fixture(t)
			command(t, native("onto"), "new", "feature", "--repo", "source", "--dir", l.ConfigRoot)
			w, err := workspace.CreateWorktree(l, "onto", "feature", "source", "main", "work/feature")
			if err != nil {
				t.Fatal(err)
			}
			command(t, native("onto"), "abandon", "feature", "--dir", l.ConfigRoot)
			p := preview(t, l, "onto", "feature", "") // a previously safe preview is not authorization
			switch kind {
			case "dirty":
				write(t, filepath.Join(w.Path, "user-work"), "keep\n")
			case "ignored":
				write(t, filepath.Join(w.Path, ".gitignore"), "private\n")
				git(t, w.Path, "add", ".gitignore")
				git(t, w.Path, "commit", "-m", "ignore")
				git(t, l.Repos["source"], "merge", "--ff-only", w.Branch)
				write(t, filepath.Join(w.Path, "private"), "keep\n")
			case "record-dirty":
				write(t, filepath.Join(l.WorkflowRoot, p.Record, "notes.md"), "uncommitted user notes\n")
			case "record-hidden":
				rel := p.Record + "/onto-state.yaml"
				git(t, l.WorkflowRoot, "update-index", "--assume-unchanged", rel)
			case "symlink":
				if err := os.Symlink(l.Repos["source"], filepath.Join(l.WorkflowRoot, p.Record, "foreign")); err != nil {
					t.Fatal(err)
				}
			}
			before := git(t, l.WorkflowRoot, "rev-parse", "HEAD")
			got, err := workflowcleanup.Apply(context.Background(), l, "onto", "feature", p.Identity, true)
			if err == nil || got.Status != "blocked" {
				t.Fatalf("unsafe cleanup accepted: %+v, %v", got, err)
			}
			if _, err := os.Stat(w.Path); err != nil {
				t.Fatal("refused cleanup removed the worktree")
			}
			if git(t, l.WorkflowRoot, "rev-parse", "HEAD") != before {
				t.Fatal("refused cleanup changed history")
			}
		})
	}
}

func TestCleanupPendingRetryAndReceiptSchema(t *testing.T) {
	l := fixture(t)
	command(t, native("to"), "new", "feature", "--repo", "source", "--dir", l.ConfigRoot)
	if _, err := workspace.CreateWorktree(l, "to", "feature", "source", "main", "work/feature"); err != nil {
		t.Fatal(err)
	}
	command(t, native("to"), "abandon", "feature", "--dir", l.ConfigRoot)
	p := preview(t, l, "to", "feature", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := workflowcleanup.Apply(ctx, l, "to", "feature", p.Identity, true); err == nil {
		t.Fatal("cancelled cleanup succeeded")
	}
	files, err := filepath.Glob(filepath.Join(l.WorkflowRoot, ".workflow", "cleanup", "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("pending receipt missing: %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var receipt workflowcleanup.Receipt
	if err := json.Unmarshal(data, &receipt); err != nil || receipt.Status != "cleanup-pending" || receipt.SchemaVersion != 1 || receipt.Identity != p.Identity {
		t.Fatalf("bad pending receipt: %+v, %v", receipt, err)
	}
	got, err := workflowcleanup.Apply(context.Background(), l, "to", "feature", p.Identity, true)
	if err != nil || got.Status != "cleanup-complete" {
		t.Fatalf("retry failed: %+v, %v", got, err)
	}
	// Neither an unknown schema nor a planted receipt may be silently replaced.
	write(t, files[0], strings.Replace(string(data), `"schema_version": 1`, `"schema_version": 999`, 1))
	if got := preview(t, l, "to", "feature", p.Identity); got.Status != "blocked" {
		t.Fatalf("unknown schema was trusted: %+v", got)
	}
}

func TestCleanupJSONStableAndUnknownGenerationReadOnly(t *testing.T) {
	l := fixture(t)
	command(t, native("to"), "new", "feature", "--repo", "source", "--dir", l.ConfigRoot)
	command(t, native("to"), "abandon", "feature", "--dir", l.ConfigRoot)
	first, err := workflowcleanup.List(l, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := workflowcleanup.List(l, "")
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("inventory drift: %v\n%+v\n%+v", err, first, second)
	}
	if _, err := workflowcleanup.Preview(l, "to", "../feature", ""); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if _, err := workflowcleanup.Apply(context.Background(), l, "to", "feature", "nonexistent", true); err == nil {
		t.Fatal("missing generation accepted")
	}
	for _, p := range first.Items {
		if p.SchemaVersion != 1 || p.Worktrees == nil || p.Blockers == nil {
			t.Fatalf("unstable JSON collections: %+v", p)
		}
	}
}

// TestCleanupRemovesUnmergedWorktreeKeepingBranchAndTmp covers ADR 0086: a
// closed change whose commits only reached its own branch (a PR delivery not
// yet merged) is cleaned at once. The branch keeps the commits, and the
// change's own scratch folder goes with the worktree; other scratch stays.
func TestCleanupRemovesUnmergedWorktreeKeepingBranchAndTmp(t *testing.T) {
	l := fixture(t)
	cfg, err := os.ReadFile(l.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	write(t, l.ConfigPath, string(cfg)+"[tmp]\ndir = '.tmp'\n")
	command(t, native("onto"), "new", "feature", "--repo", "source", "--dir", l.ConfigRoot)
	w, err := workspace.CreateWorktree(l, "onto", "feature", "source", "main", "work/feature")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(w.Path, "new-work"), "keep\n")
	git(t, w.Path, "add", "new-work")
	git(t, w.Path, "commit", "-m", "unmerged")
	tip := git(t, w.Path, "rev-parse", "HEAD")
	own := filepath.Join(l.ConfigRoot, ".tmp", "onto", "feature")
	other := filepath.Join(l.ConfigRoot, ".tmp", "onto", "other")
	write(t, filepath.Join(own, "draft.md"), "scratch\n")
	write(t, filepath.Join(other, "draft.md"), "someone else's\n")
	command(t, native("onto"), "abandon", "feature", "--dir", l.ConfigRoot)

	p := preview(t, l, "onto", "feature", "")
	if p.Status != "cleanup-pending" || p.Tmp != own {
		t.Fatalf("preview = %+v, want pending with tmp %s", p, own)
	}
	got, err := workflowcleanup.Apply(context.Background(), l, "onto", "feature", p.Identity, true)
	if err != nil || got.Status != "cleanup-complete" {
		t.Fatalf("cleanup = %+v, %v", got, err)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree not removed: %v", err)
	}
	if got := git(t, l.Repos["source"], "rev-parse", "refs/heads/"+w.Branch); got != tip {
		t.Fatalf("branch %s = %s, want retained tip %s", w.Branch, got, tip)
	}
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatalf("change scratch not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "draft.md")); err != nil {
		t.Fatalf("other change's scratch removed: %v", err)
	}
}

// TestTmpCleanKeepsOnlyLiveChangeScratch: homonto tmp clean previews, then
// removes every scratch entry except the folders of active onto/to changes.
func TestTmpCleanKeepsOnlyLiveChangeScratch(t *testing.T) {
	l := fixture(t)
	if _, err := workflowcleanup.TmpClean(l, false); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("undeclared [tmp] = %v, want refusal", err)
	}
	cfg, err := os.ReadFile(l.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	write(t, l.ConfigPath, string(cfg)+"[tmp]\ndir = '.tmp'\n")
	command(t, native("onto"), "new", "live", "--repo", "source", "--dir", l.ConfigRoot)
	command(t, native("onto"), "new", "gone", "--repo", "source", "--dir", l.ConfigRoot)
	command(t, native("onto"), "abandon", "gone", "--dir", l.ConfigRoot)
	tmp := filepath.Join(l.ConfigRoot, ".tmp")
	for _, rel := range []string{"onto/live/a.md", "onto/gone/a.md", "to/never/a.md", "h/pr-12/body.md", "loose.txt"} {
		write(t, filepath.Join(tmp, filepath.FromSlash(rel)), "scratch\n")
	}

	out := command(t, cli.NewRootCmd(), "tmp", "clean", "--config", l.ConfigPath)
	if !strings.Contains(out, "would remove onto/gone") || !strings.Contains(out, "kept onto/live") {
		t.Fatalf("preview output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "onto", "gone", "a.md")); err != nil {
		t.Fatalf("preview removed scratch: %v", err)
	}
	r, err := workflowcleanup.TmpClean(l, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"h", "loose.txt", "onto/gone", "to/never"}
	if strings.Join(r.Removed, ",") != strings.Join(want, ",") || strings.Join(r.Kept, ",") != "onto/live" {
		t.Fatalf("report = %+v, want removed %v and kept onto/live", r, want)
	}
	for _, rel := range want {
		if _, err := os.Lstat(filepath.Join(tmp, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "onto", "live", "a.md")); err != nil {
		t.Fatalf("live change scratch removed: %v", err)
	}
}
