package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wtRecordBase(t *testing.T, l Layout, workflow, repo, branch string) {
	t.Helper()
	path := filepath.Join(worktreeStatesDir(l, workflow), "feature", workflow+"-state.yaml")
	state := string(wtRead(t, path)) + fmt.Sprintf("repo_mode: explicit\nrepo_bases:\n  %s:\n    base_ref: %q\n    base_branch: %q\n    git_common_dir: %q\n", repo, wtGit(t, l.Repos[repo], "rev-parse", branch+"^{commit}"), branch, wtGit(t, l.Repos[repo], "rev-parse", "--path-format=absolute", "--git-common-dir"))
	wtWrite(t, path, state)
}

func TestReceiverIndependentAllocationPreservesDirtyOriginal(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		t.Run(workflow, func(t *testing.T) {
			l := wtFixture(t, workflow)
			dir := l.Repos["b"]
			wtRecordBase(t, l, workflow, "b", "develop")
			wtGit(t, dir, "switch", "-c", "original-work")
			wtWrite(t, filepath.Join(dir, "tracked"), "staged original\n")
			wtGit(t, dir, "add", "tracked")
			wtWrite(t, filepath.Join(dir, "tracked"), "unstaged original\n")
			wtWrite(t, filepath.Join(dir, "untracked"), "preserve\n")
			index := wtRead(t, filepath.Join(dir, ".git", "index"))
			head, branch := wtGit(t, dir, "rev-parse", "HEAD"), wtGit(t, dir, "symbolic-ref", "HEAD")
			w, err := ReceiverWorktree(l, workflow, "feature", "b")
			if err != nil {
				t.Fatal(err)
			}
			if w.Role != "receiver" || w.Branch != "develop" || w.BaseTarget != "refs/heads/develop" || w.BaseCommit != head || w.Path != filepath.Join(l.WorktreesDir, "b", ".receivers", workflow+"-feature") {
				t.Fatalf("receiver: %+v", w)
			}
			if again, err := ReceiverWorktree(l, workflow, "feature", "b"); err != nil || again != w {
				t.Fatalf("retry: %+v %v", again, err)
			}
			dirs, err := SourceDirs(l.ConfigRoot, workflow, "feature", []string{"b"})
			if err != nil || dirs["b"] != dir {
				t.Fatalf("receiver replaced execution authority: %v %v", dirs, err)
			}
			if err := cleanWorktree(w.Path); err != nil {
				t.Fatal(err)
			}
			_, err = RemoveWorktreeRole(l, workflow, "feature", "b", "receiver", true)
			wtError(t, err, "active binding")
			wtState(t, l, workflow, "feature", "generation-one", true, "a", "b")
			if _, err := RemoveWorktreeRole(l, workflow, "feature", "b", "receiver", true); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(index, wtRead(t, filepath.Join(dir, ".git", "index"))) || string(wtRead(t, filepath.Join(dir, "tracked"))) != "unstaged original\n" || string(wtRead(t, filepath.Join(dir, "untracked"))) != "preserve\n" || head != wtGit(t, dir, "rev-parse", "HEAD") || branch != wtGit(t, dir, "symbolic-ref", "HEAD") {
				t.Fatal("receiver lifecycle changed dirty original")
			}
		})
	}
}

func TestReceiverAndExecutionRolesIntegrateAndRemoveIndependently(t *testing.T) {
	for _, first := range []string{"execution", "receiver"} {
		t.Run(first, func(t *testing.T) {
			l := wtFixture(t, "to")
			execution := wtCreate(t, l, "to", "a", "main")
			wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
			receiver, err := ReceiverWorktree(l, "to", "feature", "a")
			if err != nil {
				t.Fatal(err)
			}
			wtWrite(t, filepath.Join(execution.Path, "tracked"), "implemented\n")
			wtGit(t, execution.Path, "commit", "-am", "implement")
			wtGit(t, receiver.Path, "merge", "--ff-only", execution.Branch)
			if dirs, err := SourceDirs(l.ConfigRoot, "to", "feature", []string{"a"}); err != nil || dirs["a"] != execution.Path {
				t.Fatalf("roles crossed: %v %v", dirs, err)
			}
			if entries, err := ListWorktrees(l); err != nil || len(entries) != 2 {
				t.Fatalf("list both roles: %+v %v", entries, err)
			}
			wtState(t, l, "to", "feature", "generation-one", true, "a", "b")
			active := filepath.Join(worktreeStatesDir(l, "to"), "feature")
			archive := filepath.Join(worktreeStatesDir(l, "to"), "archive", "2026-09-08-feature")
			if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(active, archive); err != nil {
				t.Fatal(err)
			}
			second := "receiver"
			if first == "receiver" {
				second = "execution"
			}
			for i, role := range []string{first, second} {
				if _, err := RemoveWorktreeRole(l, "to", "feature", "a", role, true); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					wtError(t, EnsureNameAvailable(l, "to", "feature"), "registered binding")
					if entries, err := ListWorktrees(l); err != nil || len(entries) != 1 || entries[0].Role != second {
						t.Fatalf("wrong role removed: %+v %v", entries, err)
					}
				}
			}
			if err := EnsureNameAvailable(l, "to", "feature"); err != nil {
				t.Fatal(err)
			}
			wtGit(t, l.Repos["a"], "show-ref", "--verify", "refs/heads/"+execution.Branch)
			wtGit(t, l.Repos["a"], "show-ref", "--verify", receiver.BaseTarget)
		})
	}
}

func TestReceiverFailsClosed(t *testing.T) {
	for _, scenario := range []string{"no-recorded-target", "unselected", "checked-out-clean", "checked-out-dirty", "linked-target", "path-occupied", "symlink", "wrong-identity", "scope-mode", "missing-target", "terminal", "registry-lock"} {
		t.Run(scenario, func(t *testing.T) {
			l := wtFixture(t, "onto")
			dir := l.Repos["a"]
			if scenario != "no-recorded-target" {
				wtRecordBase(t, l, "onto", "a", "main")
			}
			if scenario != "checked-out-clean" && scenario != "checked-out-dirty" {
				wtGit(t, dir, "switch", "-c", "original-work")
			}
			want := "recorded local base"
			dest := filepath.Join(l.WorktreesDir, "a", ".receivers", "onto-feature")
			switch scenario {
			case "unselected":
				wtState(t, l, "onto", "feature", "generation-one", false, "b")
				want = "not selected"
			case "checked-out-clean":
				want = "already checked out"
			case "checked-out-dirty":
				wtWrite(t, filepath.Join(dir, "tracked"), "keep dirty\n")
				want = "already checked out"
			case "linked-target":
				wtGit(t, dir, "worktree", "add", filepath.Join(l.ConfigRoot, "user-owned"), "main")
				want = "already checked out"
			case "path-occupied":
				wtWrite(t, filepath.Join(dest, "keep"), "unknown")
				want = "refusing overwrite"
			case "symlink":
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), dest); err != nil {
					t.Fatal(err)
				}
				want = "unsafe path"
			case "wrong-identity":
				l.Repos["a"] = l.Repos["b"]
				want = "common-dir mismatch"
			case "scope-mode":
				l.SchemaVersion = 1
				want = "scope mode changed"
			case "missing-target":
				wtGit(t, dir, "branch", "-d", "main")
				want = "no longer contains"
			case "terminal":
				wtState(t, l, "onto", "feature", "generation-one", true, "a")
				want = "recorded local base"
			case "registry-lock":
				unlock, err := lockWorktreeRegistry(l)
				if err != nil {
					t.Fatal(err)
				}
				defer unlock()
				want = "registry lock"
			}
			index := wtRead(t, filepath.Join(dir, ".git", "index"))
			refs := wtGit(t, dir, "show-ref")
			_, err := ReceiverWorktree(l, "onto", "feature", "a")
			wtError(t, err, want)
			if !bytes.Equal(index, wtRead(t, filepath.Join(dir, ".git", "index"))) || refs != wtGit(t, dir, "show-ref") {
				t.Fatal("blocked receiver modified original")
			}
			if _, err := os.Stat(registryPath(l)); !os.IsNotExist(err) {
				t.Fatalf("blocked preflight wrote registry: %v", err)
			}
		})
	}
}

func TestReceiverRetryAndRemovalRefuseChangedOwnershipOrDirt(t *testing.T) {
	for _, scenario := range []string{"dirty", "ignored", "hidden-index", "operation", "replaced", "reused-name", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			l := wtFixture(t, "to")
			wtRecordBase(t, l, "to", "a", "main")
			wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
			w, err := ReceiverWorktree(l, "to", "feature", "a")
			if err != nil {
				t.Fatal(err)
			}
			want := "dirty registered path"
			switch scenario {
			case "dirty":
				wtWrite(t, filepath.Join(w.Path, "tracked"), "dirty\n")
			case "ignored":
				wtWrite(t, filepath.Join(w.CommonDir, "info", "exclude"), "ignored\n")
				wtWrite(t, filepath.Join(w.Path, "ignored"), "keep")
			case "hidden-index":
				wtGit(t, w.Path, "update-index", "--assume-unchanged", "tracked")
				wtWrite(t, filepath.Join(w.Path, "tracked"), "hidden\n")
				want = "nondefault index flags"
			case "operation":
				wtWrite(t, filepath.Join(w.GitDir, "CHERRY_PICK_HEAD"), w.BaseCommit+"\n")
				want = "Git operation"
			case "replaced":
				wtGit(t, l.Repos["a"], "worktree", "remove", w.Path)
				wtGit(t, l.Repos["a"], "worktree", "add", w.Path, "main")
				want = "creation token mismatch"
			case "reused-name":
				wtState(t, l, "to", "feature", "generation-two", false, "a")
				want = "state identity mismatch"
			case "pending":
				w.Status = "creating"
				if err := saveWorktreeRegistry(l, worktreeRegistry{Version: 1, Entries: []Worktree{w}}); err != nil {
					t.Fatal(err)
				}
				want = "incomplete registry"
			}
			_, err = ReceiverWorktree(l, "to", "feature", "a")
			wtError(t, err, want)
			id := "generation-one"
			if scenario == "reused-name" {
				id = "generation-two"
			}
			wtState(t, l, "to", "feature", id, true, "a")
			registry, index := wtRead(t, registryPath(l)), wtRead(t, filepath.Join(w.GitDir, "index"))
			_, err = RemoveWorktreeRole(l, "to", "feature", "a", "receiver", true)
			wtError(t, err, want)
			if !bytes.Equal(registry, wtRead(t, registryPath(l))) || !bytes.Equal(index, wtRead(t, filepath.Join(w.GitDir, "index"))) {
				t.Fatal("blocked lifecycle changed registry or index")
			}
		})
	}
}

func TestWorktreeLifecycleExcludesStateWritersBeforeRead(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		l := wtFixture(t, workflow)
		for _, path := range []string{filepath.Join(worktreeStatesDir(l, workflow), "."+workflow+".lock"), filepath.Join(l.WorkflowRoot, ".change-names.lock")} {
			unlock, err := lockWorktreeLifecyclePath(path)
			if err != nil {
				t.Fatal(err)
			}
			// An invalid state must not be read before either shared lock.
			state := filepath.Join(worktreeStatesDir(l, workflow), "feature", workflow+"-state.yaml")
			before := wtRead(t, state)
			wtWrite(t, state, "invalid: [")
			_, err = CreateWorktree(l, workflow, "feature", "a", "main", "work/test")
			wtError(t, err, "lifecycle/name lock")
			_, err = ReceiverWorktree(l, workflow, "feature", "a")
			wtError(t, err, "lifecycle/name lock")
			wtWrite(t, state, string(before))
			unlock()
		}
		wtCreate(t, l, workflow, "a", "main")
	}
}

func TestWorktreeConcurrentRoleAllocation(t *testing.T) {
	l := wtFixture(t, "to")
	wtRecordBase(t, l, "to", "a", "main")
	wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := CreateWorktree(l, "to", "feature", "a", "main", "work/feature-a")
		results <- err
	}()
	go func() { <-start; _, err := ReceiverWorktree(l, "to", "feature", "a"); results <- err }()
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			wtError(t, err, "lifecycle/name lock")
		}
	}
	// Fail-fast contenders can retry, but never install duplicate role keys.
	wtCreate(t, l, "to", "a", "main")
	if _, err := ReceiverWorktree(l, "to", "feature", "a"); err != nil {
		t.Fatal(err)
	}
	if entries, err := ListWorktrees(l); err != nil || len(entries) != 2 {
		t.Fatalf("concurrent registry: %+v %v", entries, err)
	}
}

func TestReceiverAcceptedLegacyStateFilename(t *testing.T) {
	l := wtFixture(t, "onto")
	dir := l.Repos["a"]
	wtRecordBase(t, l, "onto", "a", "main")
	base := wtGit(t, dir, "rev-parse", "HEAD")
	stateDir := filepath.Join(worktreeStatesDir(l, "onto"), "feature")
	if err := os.Rename(filepath.Join(stateDir, "onto-state.yaml"), filepath.Join(stateDir, "state.yaml")); err != nil {
		t.Fatal(err)
	}
	wtGit(t, dir, "switch", "-c", "original-work")
	w, err := ReceiverWorktree(l, "onto", "feature", "a")
	if err != nil || w.BaseCommit != base || w.BaseTarget != "refs/heads/main" {
		t.Fatalf("legacy independent receiver: %+v %v", w, err)
	}
}

func wtArchiveFeature(t *testing.T, l Layout, workflow, entry string) string {
	t.Helper()
	archive := filepath.Join(worktreeStatesDir(l, workflow), "archive", entry)
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(worktreeStatesDir(l, workflow), "feature"), archive); err != nil {
		t.Fatal(err)
	}
	return archive
}

func TestReceiverTerminalAndArchivedRecovery(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		for _, priorRole := range []string{"none", "execution", "receiver"} {
			for _, archived := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/archived=%v", workflow, priorRole, archived), func(t *testing.T) {
					l := wtFixture(t, workflow)
					wtRecordBase(t, l, workflow, "a", "main")
					if priorRole == "execution" {
						wtCreate(t, l, workflow, "a", "main")
					}
					dir := l.Repos["a"]
					wtGit(t, dir, "switch", "-c", "original-work")
					var prior Worktree
					if priorRole == "receiver" {
						var err error
						prior, err = ReceiverWorktree(l, workflow, "feature", "a")
						if err != nil {
							t.Fatal(err)
						}
					}
					wtState(t, l, workflow, "feature", "generation-one", true, "a", "b")
					wtRecordBase(t, l, workflow, "a", "main")
					if archived {
						wtArchiveFeature(t, l, workflow, "2026-09-08-feature")
					}
					wtWrite(t, filepath.Join(dir, "tracked"), "staged original\n")
					wtGit(t, dir, "add", "tracked")
					wtWrite(t, filepath.Join(dir, "tracked"), "unstaged original\n")
					wtWrite(t, filepath.Join(dir, "untracked"), "preserve\n")
					index := wtRead(t, filepath.Join(dir, ".git", "index"))
					refs, head, branch := wtGit(t, dir, "show-ref"), wtGit(t, dir, "rev-parse", "HEAD"), wtGit(t, dir, "symbolic-ref", "HEAD")
					w, err := ReceiverWorktree(l, workflow, "feature", "a")
					if err != nil {
						t.Fatal(err)
					}
					if w.StateID != "id:generation-one" || w.Role != "receiver" || w.Status != "ready" {
						t.Fatalf("recovered receiver: %+v", w)
					}
					if priorRole == "receiver" && prior != w {
						t.Fatalf("terminal recovery changed binding: %+v != %+v", prior, w)
					}
					if again, err := ReceiverWorktree(l, workflow, "feature", "a"); err != nil || again != w {
						t.Fatalf("recovery retry: %+v %v", again, err)
					}
					_, err = CreateWorktree(l, workflow, "feature", "a", "main", "work/new-terminal-execution")
					wtError(t, err, "matching active state")
					if _, err := RemoveWorktreeRole(l, workflow, "feature", "a", "receiver", true); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(index, wtRead(t, filepath.Join(dir, ".git", "index"))) || refs != wtGit(t, dir, "show-ref") || head != wtGit(t, dir, "rev-parse", "HEAD") || branch != wtGit(t, dir, "symbolic-ref", "HEAD") || string(wtRead(t, filepath.Join(dir, "tracked"))) != "unstaged original\n" || string(wtRead(t, filepath.Join(dir, "untracked"))) != "preserve\n" {
						t.Fatal("receiver recovery changed dirty original")
					}
					if archived {
						if _, err := os.Stat(filepath.Join(worktreeStatesDir(l, workflow), "feature")); !os.IsNotExist(err) {
							t.Fatal("receiver recovery recreated active state")
						}
					}
				})
			}
		}
	}
}

func TestReceiverArchiveGenerationSelection(t *testing.T) {
	for _, workflow := range []string{"onto", "to"} {
		for _, scenario := range []string{"unbound-ambiguous", "execution", "receiver", "duplicate-id", "reused-active-name", "nonterminal-archive"} {
			t.Run(workflow+"/"+scenario, func(t *testing.T) {
				l := wtFixture(t, workflow)
				wtRecordBase(t, l, workflow, "a", "main")
				if scenario == "execution" || scenario == "duplicate-id" || scenario == "reused-active-name" {
					wtCreate(t, l, workflow, "a", "main")
				}
				wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
				if scenario == "receiver" {
					if _, err := ReceiverWorktree(l, workflow, "feature", "a"); err != nil {
						t.Fatal(err)
					}
				}
				wtState(t, l, workflow, "feature", "generation-one", scenario != "nonterminal-archive", "a", "b")
				wtRecordBase(t, l, workflow, "a", "main")
				first := wtArchiveFeature(t, l, workflow, "first-feature")
				if scenario != "nonterminal-archive" {
					id := "generation-two"
					if scenario == "duplicate-id" {
						id = "generation-one"
					}
					wtState(t, l, workflow, "feature", id, scenario != "reused-active-name", "a", "b")
					wtRecordBase(t, l, workflow, "a", "main")
					if scenario != "reused-active-name" {
						wtArchiveFeature(t, l, workflow, "second-feature")
					}
				}
				wtWrite(t, filepath.Join(l.Repos["a"], "tracked"), "original stays dirty\n")
				index := wtRead(t, filepath.Join(l.Repos["a"], ".git", "index"))
				statePath := filepath.Join(first, workflow+"-state.yaml")
				before := wtRead(t, statePath)
				w, err := ReceiverWorktree(l, workflow, "feature", "a")
				switch scenario {
				case "execution", "receiver":
					if err != nil || w.StateID != "id:generation-one" {
						t.Fatalf("wrong archived generation: %+v %v", w, err)
					}
					if again, err := ReceiverWorktree(l, workflow, "feature", "a"); err != nil || again != w {
						t.Fatalf("ambiguous-name retry lost bound identity: %+v %v", again, err)
					}
					_, err = ReceiverWorktree(l, workflow, "feature", "a", "generation-two")
					wtError(t, err, "disagree on state identity")
				case "reused-active-name":
					wtError(t, err, "state identity mismatch")
					_, err = ReceiverWorktree(l, workflow, "feature", "a", "generation-one")
					wtError(t, err, "state identity mismatch")
				case "nonterminal-archive":
					wtError(t, err, "not terminal")
				default:
					wtError(t, err, "found 2")
				}
				if scenario == "unbound-ambiguous" {
					_, err = ReceiverWorktree(l, workflow, "feature", "a", "missing-generation")
					wtError(t, err, "found 0")
					w, err = ReceiverWorktree(l, workflow, "feature", "a", "generation-two")
					if err != nil || w.StateID != "id:generation-two" {
						t.Fatalf("explicit archive selection: %+v %v", w, err)
					}
					if again, err := ReceiverWorktree(l, workflow, "feature", "a"); err != nil || again != w {
						t.Fatalf("selected identity did not persist: %+v %v", again, err)
					}
					_, err = ReceiverWorktree(l, workflow, "feature", "a", "id:generation-one")
					wtError(t, err, "disagree on state identity")
				}
				if !bytes.Equal(before, wtRead(t, statePath)) || !bytes.Equal(index, wtRead(t, filepath.Join(l.Repos["a"], ".git", "index"))) || string(wtRead(t, filepath.Join(l.Repos["a"], "tracked"))) != "original stays dirty\n" {
					t.Fatal("archive selection modified state or original checkout")
				}
			})
		}
	}
}

func TestReceiverRejectsInconsistentRoleBindings(t *testing.T) {
	for _, scenario := range []string{"identity", "base"} {
		t.Run(scenario, func(t *testing.T) {
			l := wtFixture(t, "to")
			wtCreate(t, l, "to", "a", "main")
			wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
			if _, err := ReceiverWorktree(l, "to", "feature", "a"); err != nil {
				t.Fatal(err)
			}
			r, err := readWorktreeRegistry(l)
			if err != nil {
				t.Fatal(err)
			}
			for i := range r.Entries {
				if r.Entries[i].Role == "receiver" {
					if scenario == "identity" {
						r.Entries[i].StateID = "id:other-generation"
					} else {
						r.Entries[i].BaseCommit = strings.Repeat("0", 40)
					}
				}
			}
			if err := saveWorktreeRegistry(l, r); err != nil {
				t.Fatal(err)
			}
			before := wtRead(t, registryPath(l))
			_, err = ReceiverWorktree(l, "to", "feature", "a")
			wtError(t, err, "disagree")
			if !bytes.Equal(before, wtRead(t, registryPath(l))) {
				t.Fatal("inconsistent roles were rebound")
			}
		})
	}
}

func TestReceiverConcurrentArchivedRecovery(t *testing.T) {
	l := wtFixture(t, "to")
	wtState(t, l, "to", "feature", "generation-one", true, "a")
	wtRecordBase(t, l, "to", "a", "main")
	wtArchiveFeature(t, l, "to", "2026-09-08-feature")
	wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
	start := make(chan struct{})
	type result struct {
		w   Worktree
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() { <-start; w, err := ReceiverWorktree(l, "to", "feature", "a"); results <- result{w, err} }()
	}
	close(start)
	var installed Worktree
	for range 2 {
		r := <-results
		if r.err != nil {
			wtError(t, r.err, "lifecycle/name lock")
			continue
		}
		if installed.Path != "" && installed != r.w {
			t.Fatal("concurrent recovery created two bindings")
		}
		installed = r.w
	}
	if installed.Path == "" {
		t.Fatal("neither recovery installed a receiver")
	}
	if again, err := ReceiverWorktree(l, "to", "feature", "a"); err != nil || again != installed {
		t.Fatalf("retry after concurrent recovery: %+v %v", again, err)
	}
	if entries, err := ListWorktrees(l); err != nil || len(entries) != 1 {
		t.Fatalf("archive registry: %+v %v", entries, err)
	}
}

func TestExecutionCreationRespectsReceiverGenerationAndBase(t *testing.T) {
	for _, scenario := range []string{"matching", "reused-id", "different-base"} {
		t.Run(scenario, func(t *testing.T) {
			l := wtFixture(t, "to")
			wtRecordBase(t, l, "to", "a", "main")
			wtGit(t, l.Repos["a"], "switch", "-c", "original-work")
			if _, err := ReceiverWorktree(l, "to", "feature", "a"); err != nil {
				t.Fatal(err)
			}
			base, want := "main", "state identity mismatch"
			switch scenario {
			case "reused-id":
				wtState(t, l, "to", "feature", "generation-two", false, "a")
			case "different-base":
				wtState(t, l, "to", "feature", "generation-one", false, "a")
				wtGit(t, l.Repos["a"], "branch", "alternate-base", "main")
				base, want = "alternate-base", "integration base disagrees"
			}
			before := wtRead(t, registryPath(l))
			refs := wtGit(t, l.Repos["a"], "show-ref")
			_, err := CreateWorktree(l, "to", "feature", "a", base, "work/feature-a")
			if scenario == "matching" {
				if err != nil {
					t.Fatal(err)
				}
				if entries, err := ListWorktrees(l); err != nil || len(entries) != 2 {
					t.Fatalf("compatible roles: %+v %v", entries, err)
				}
				return
			}
			wtError(t, err, want)
			if !bytes.Equal(before, wtRead(t, registryPath(l))) || refs != wtGit(t, l.Repos["a"], "show-ref") {
				t.Fatal("execution creation changed receiver ownership or source refs")
			}
		})
	}
}
