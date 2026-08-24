package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// TestCreateAssignmentRejectsDirtyMember proves ADR 0024's reject-dirty-at-
// work-start rule: a member with uncommitted changes (modified, staged, or
// untracked) is refused with the dirty paths named, and nothing is
// journaled.
func TestCreateAssignmentRejectsDirtyMember(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, e *env)
		file   string
	}{
		{"modified tracked file", func(t *testing.T, e *env) {
			if err := os.WriteFile(filepath.Join(e.member, "seed.txt"), []byte("dirty\n"), 0o644); err != nil {
				t.Fatalf("gitx: write: %v", err)
			}
		}, "seed.txt"},
		{"staged change", func(t *testing.T, e *env) {
			if err := os.WriteFile(filepath.Join(e.member, "seed.txt"), []byte("staged\n"), 0o644); err != nil {
				t.Fatalf("gitx: write: %v", err)
			}
			if _, err := (ExecRunner{}).Run(context.Background(), e.member, "add", "seed.txt"); err != nil {
				t.Fatalf("gitx: stage: %v", err)
			}
		}, "seed.txt"},
		{"untracked file", func(t *testing.T, e *env) {
			if err := os.WriteFile(filepath.Join(e.member, "new.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatalf("gitx: write: %v", err)
			}
		}, "new.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.mutate(t, e)
			_, err := e.svc.CreateAssignment(context.Background(), CreateRequest{
				WorkID:        e.workID,
				ActionID:      newAction(t),
				RepositoryID:  e.repoID,
				RepositoryDir: e.member,
				BaseCommit:    e.base(t),
			})
			var de *DirtyWorktreeError
			if !errors.As(err, &de) {
				t.Fatalf("CreateAssignment error = %v, want *DirtyWorktreeError", err)
			}
			if !errors.Is(err, ErrDirtyWorktree) {
				t.Error("errors.Is(err, ErrDirtyWorktree) = false, want true")
			}
			if len(de.Files) != 1 || de.Files[0] != tc.file {
				t.Errorf("dirty files = %v, want [%s]", de.Files, tc.file)
			}
			if n := opCount(t, e.db, OpKindCreateWorktree); n != 0 {
				t.Errorf("journaled %d create ops for a dirty member, want 0", n)
			}
		})
	}
}

// TestCreateAssignmentNamesBranchAndPath proves the branch/path contract:
// homonto/work/<work>/<action> under .homonto/worktrees/<repo>/<action>,
// created without touching the member's main working tree.
func TestCreateAssignmentNamesBranchAndPath(t *testing.T) {
	e := newEnv(t)
	action := newAction(t)
	wt := e.assign(t, action, []string{"src"})

	wantPath := WorktreePath(e.root, e.repoID, action)
	wantBranch := WorktreeBranch(e.workID, action)
	if wt.Path != wantPath {
		t.Errorf("Path = %q, want %q", wt.Path, wantPath)
	}
	if wt.Branch != wantBranch {
		t.Errorf("Branch = %q, want %q", wt.Branch, wantBranch)
	}
	if wt.BaseCommit != e.base(t) {
		t.Errorf("BaseCommit = %q, want %q", wt.BaseCommit, e.base(t))
	}

	// The worktree exists on disk with a .git file (a linked worktree is
	// attached to the member's common directory, never a copy).
	if st, err := os.Stat(filepath.Join(wt.Path, ".git")); err != nil {
		t.Fatalf("stat .git in worktree: %v", err)
	} else if st.IsDir() {
		t.Error(".git in worktree is a directory, want a file")
	}
	out, err := ExecRunner{}.Run(context.Background(), wt.Path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if got := strings.TrimSpace(out); got != wantBranch {
		t.Errorf("worktree HEAD = %q, want %q", got, wantBranch)
	}

	// The member's main working tree is untouched and still clean, and the
	// branch ref exists in the member.
	if got := porcelain(t, e.member); got != "" {
		t.Errorf("member main tree dirty after worktree creation: %q", got)
	}
	if data, err := os.ReadFile(filepath.Join(e.member, "seed.txt")); err != nil || string(data) != "seed\n" {
		t.Errorf("member seed.txt = %q (err %v), want seed\n", data, err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "show-ref", "--verify", "--quiet", "refs/heads/"+wantBranch); err != nil {
		t.Errorf("branch ref refs/heads/%s missing: %v", wantBranch, err)
	}

	// The assignment is journaled as one finalized operation.
	id, state := latestOp(t, e.db, OpKindCreateWorktree)
	if state != store.OpFinalized {
		t.Errorf("create op %s state = %q, want %q", id, state, store.OpFinalized)
	}
}

func TestValidateResultHappyPath(t *testing.T) {
	e := newEnv(t)
	action := newAction(t)
	scope := []string{"src"}
	wt := e.assign(t, action, scope)
	sha := implement(t, wt, "src/a.go", "package a\n", "implement a")

	m, err := e.svc.ValidateResult(context.Background(), wt, scope)
	if err != nil {
		t.Fatalf("ValidateResult: %v", err)
	}
	if m.Commit != sha {
		t.Errorf("Commit = %q, want %q", m.Commit, sha)
	}
	if m.BaseCommit != wt.BaseCommit {
		t.Errorf("BaseCommit = %q, want %q", m.BaseCommit, wt.BaseCommit)
	}
	if m.ActionID != action {
		t.Errorf("ActionID = %s, want %s", m.ActionID, action)
	}
	if m.RepositoryID != e.repoID {
		t.Errorf("RepositoryID = %s, want %s", m.RepositoryID, e.repoID)
	}
	if len(m.ChangedPaths) != 1 || m.ChangedPaths[0] != "src/a.go" {
		t.Errorf("ChangedPaths = %v, want [src/a.go]", m.ChangedPaths)
	}
}

func TestValidateResultRejectsNoCommit(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), nil)
	_, err := e.svc.ValidateResult(context.Background(), wt, nil)
	if err == nil {
		t.Fatal("ValidateResult: expected error for an untouched worktree")
	}
	if !strings.Contains(err.Error(), "exactly 1") {
		t.Errorf("error = %v, want it to name the one-commit requirement", err)
	}
}

func TestValidateResultRejectsTwoCommits(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), []string{"src"})
	implement(t, wt, "src/a.go", "package a\n", "one")
	implement(t, wt, "src/b.go", "package b\n", "two")
	_, err := e.svc.ValidateResult(context.Background(), wt, []string{"src"})
	if err == nil {
		t.Fatal("ValidateResult: expected error for two commits")
	}
	if !strings.Contains(err.Error(), "exactly 1") {
		t.Errorf("error = %v, want it to name the one-commit requirement", err)
	}
}

// TestValidateResultRejectsWrongParent constructs a HEAD that is exactly one
// commit ahead of the base whose parent is not the base (a merge of two
// ancestors) and proves the parent check refuses it.
func TestValidateResultRejectsWrongParent(t *testing.T) {
	e := newEnv(t)
	// Member history: seed (root) then base.
	commitFile(t, e.member, "second.txt", "second\n", "second")
	base := e.base(t)
	r := ExecRunner{}
	ctx := context.Background()
	first, err := r.Run(ctx, e.member, "rev-parse", "HEAD^")
	if err != nil {
		t.Fatalf("gitx: rev-parse HEAD^: %v", err)
	}
	first = strings.TrimSpace(first)
	tree, err := r.Run(ctx, e.member, "rev-parse", "HEAD^{tree}")
	if err != nil {
		t.Fatalf("gitx: rev-parse HEAD tree: %v", err)
	}
	// merge = a commit whose only non-base ancestor is itself but whose
	// first parent is an ancestor, not the base.
	merge, err := r.Run(ctx, e.member, "commit-tree", strings.TrimSpace(tree), "-p", first, "-p", first, "-m", "merge")
	if err != nil {
		t.Fatalf("gitx: commit-tree: %v", err)
	}
	merge = strings.TrimSpace(merge)

	wt := e.assign(t, newAction(t), nil)
	if _, err := r.Run(ctx, wt.Path, "reset", "--hard", merge); err != nil {
		t.Fatalf("gitx: reset worktree to merge commit: %v", err)
	}
	_, err = e.svc.ValidateResult(context.Background(), wt, nil)
	if err == nil {
		t.Fatal("ValidateResult: expected error for wrong-parent HEAD")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Errorf("error = %v, want it to name the parent mismatch", err)
	}
	if base == first {
		t.Fatal("test fixture broken: base must differ from its parent")
	}
}

func TestValidateResultRejectsScopeViolation(t *testing.T) {
	e := newEnv(t)
	action := newAction(t)
	scope := []string{"src"}
	wt := e.assign(t, action, scope)
	implementAll(t, wt, map[string]string{
		"src/a.go":      "package a\n",
		"out/leak.go":   "package out\n",
		"srcx/other.go": "package other\n",
	}, "one commit, one out-of-scope path")

	_, err := e.svc.ValidateResult(context.Background(), wt, scope)
	var se *ScopeViolationError
	if !errors.As(err, &se) {
		t.Fatalf("ValidateResult error = %v, want *ScopeViolationError", err)
	}
	if !errors.Is(err, ErrScopeViolation) {
		t.Error("errors.Is(err, ErrScopeViolation) = false, want true")
	}
	// Only the truly out-of-scope paths are listed: "srcx/other.go" is not
	// under the "src" prefix, "out/leak.go" is unrelated.
	want := []string{"out/leak.go", "srcx/other.go"}
	if len(se.Files) != len(want) {
		t.Fatalf("violating files = %v, want %v", se.Files, want)
	}
	for i := range want {
		if se.Files[i] != want[i] {
			t.Errorf("violating files = %v, want %v", se.Files, want)
			break
		}
	}
}

// TestValidateResultScopeNormalization proves scope entries are cleaned
// before comparison: a trailing slash means the same directory scope, and
// "." means the whole repository.
func TestValidateResultScopeNormalization(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), []string{"src/"})
	implement(t, wt, "src/a.go", "package a\n", "in scope")
	if _, err := e.svc.ValidateResult(context.Background(), wt, []string{"src"}); err != nil {
		t.Errorf("ValidateResult with scope src/ (normalized): %v", err)
	}

	wt2 := e.assign(t, newAction(t), nil)
	implement(t, wt2, "anywhere.go", "package a\n", "whole repo")
	if _, err := e.svc.ValidateResult(context.Background(), wt2, []string{"."}); err != nil {
		t.Errorf("ValidateResult with scope .: %v", err)
	}
}

func TestValidateResultRejectsDirtyWorktree(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), []string{"src"})
	implement(t, wt, "src/a.go", "package a\n", "commit")
	if err := os.WriteFile(filepath.Join(wt.Path, "src", "a.go"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}
	_, err := e.svc.ValidateResult(context.Background(), wt, []string{"src"})
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("ValidateResult error = %v, want *DirtyWorktreeError", err)
	}
	if len(de.Files) != 1 || de.Files[0] != "src/a.go" {
		t.Errorf("dirty files = %v, want [src/a.go]", de.Files)
	}
}

// TestValidateResultRejectsEmptyCommit proves an --allow-empty commit is
// refused with the typed ErrEmptyCommitMaterial naming the action: an
// empty diff cannot be cherry-picked (git stops with "previous cherry-pick
// is now empty" and conflict continuation deadlocks).
func TestValidateResultRejectsEmptyCommit(t *testing.T) {
	e := newEnv(t)
	action := newAction(t)
	wt := e.assign(t, action, []string{"src"})
	if _, err := (ExecRunner{}).Run(context.Background(), wt.Path, "commit", "--allow-empty", "-m", "empty"); err != nil {
		t.Fatalf("gitx: allow-empty commit: %v", err)
	}

	_, err := e.svc.ValidateResult(context.Background(), wt, []string{"src"})
	var ec *EmptyCommitMaterialError
	if !errors.As(err, &ec) {
		t.Fatalf("ValidateResult error = %v, want *EmptyCommitMaterialError", err)
	}
	if !errors.Is(err, ErrEmptyCommitMaterial) {
		t.Error("errors.Is(err, ErrEmptyCommitMaterial) = false, want true")
	}
	if ec.ActionID != action {
		t.Errorf("ActionID = %s, want %s", ec.ActionID, action)
	}
}

// TestValidateResultRejectsForeignBranch proves ValidateResult verifies the
// worktree's checked-out branch: a worktree stolen onto another branch (or
// a detached commit structure) is rejected with the typed
// ErrBranchMismatch, never minted into material.
func TestValidateResultRejectsForeignBranch(t *testing.T) {
	tests := []struct {
		name    string
		steal   func(t *testing.T, dir string)
		wantGot string
	}{
		{"stolen onto another branch", func(t *testing.T, dir string) {
			if _, err := (ExecRunner{}).Run(context.Background(), dir, "checkout", "-q", "-b", "intruder"); err != nil {
				t.Fatalf("gitx: checkout intruder: %v", err)
			}
		}, "intruder"},
		{"stolen onto detached commit", func(t *testing.T, dir string) {
			if _, err := (ExecRunner{}).Run(context.Background(), dir, "checkout", "-q", "--detach"); err != nil {
				t.Fatalf("gitx: detach: %v", err)
			}
		}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			wt := e.assign(t, newAction(t), []string{"src"})
			implement(t, wt, "src/a.go", "package a\n", "implement a")
			tc.steal(t, wt.Path)

			_, err := e.svc.ValidateResult(context.Background(), wt, []string{"src"})
			var be *BranchMismatchError
			if !errors.As(err, &be) {
				t.Fatalf("ValidateResult error = %v, want *BranchMismatchError", err)
			}
			if !errors.Is(err, ErrBranchMismatch) {
				t.Error("errors.Is(err, ErrBranchMismatch) = false, want true")
			}
			if be.Want != wt.Branch || be.Got != tc.wantGot {
				t.Errorf("branch mismatch want/got = %q/%q, want %q/%q", be.Want, be.Got, wt.Branch, tc.wantGot)
			}
		})
	}
}

func TestValidateResultRejectsMissingWorktree(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), nil)
	if err := e.svc.Cleanup(context.Background(), wt.Worktree, false); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	_, err := e.svc.ValidateResult(context.Background(), wt, nil)
	if !errors.Is(err, ErrWorktreeMissing) {
		t.Fatalf("ValidateResult error = %v, want ErrWorktreeMissing", err)
	}
}

func TestCleanupRemovesWorktreeAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	action := newAction(t)
	wt := e.assign(t, action, nil)
	if err := e.svc.Cleanup(context.Background(), wt.Worktree, false); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree path %s still exists (err %v)", wt.Path, err)
	}
	if registered(t, e.svc, e.member, wt.Path) {
		t.Error("worktree still registered after cleanup")
	}
	id, state := latestOp(t, e.db, OpKindRemoveWorktree)
	if state != store.OpFinalized {
		t.Errorf("remove op %s state = %q, want %q", id, state, store.OpFinalized)
	}
	// The branch ref is left in place: integration branches are the
	// durable handoff, never deleted by cleanup.
	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "show-ref", "--verify", "--quiet", "refs/heads/"+wt.Branch); err != nil {
		t.Error("cleanup deleted the branch ref, want it left in place")
	}
	// Missing is success: a second cleanup is a no-op.
	if err := e.svc.Cleanup(context.Background(), wt.Worktree, false); err != nil {
		t.Errorf("second Cleanup: %v", err)
	}
}

func TestCleanupRefusesDirtyWorktreeUnlessForce(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), nil)
	implement(t, wt, "a.txt", "a\n", "commit")
	if err := os.WriteFile(filepath.Join(wt.Path, "a.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}

	err := e.svc.Cleanup(context.Background(), wt.Worktree, false)
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("Cleanup error = %v, want *DirtyWorktreeError", err)
	}
	if len(de.Files) != 1 || de.Files[0] != "a.txt" {
		t.Errorf("dirty files = %v, want [a.txt]", de.Files)
	}
	if !registered(t, e.svc, e.member, wt.Path) {
		t.Error("worktree unregistered after refused cleanup")
	}
	if err := e.svc.Cleanup(context.Background(), wt.Worktree, true); err != nil {
		t.Fatalf("forced Cleanup: %v", err)
	}
	if registered(t, e.svc, e.member, wt.Path) {
		t.Error("worktree still registered after forced cleanup")
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree path %s still exists after forced cleanup (err %v)", wt.Path, err)
	}
}

// TestCleanupMissingWorktreeIsSuccess cleans a worktree that was removed
// out from under the service (for example by an earlier crashed cleanup).
func TestCleanupMissingWorktreeIsSuccess(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), nil)
	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "worktree", "remove", wt.Path); err != nil {
		t.Fatalf("gitx: worktree remove: %v", err)
	}
	if err := e.svc.Cleanup(context.Background(), wt.Worktree, false); err != nil {
		t.Fatalf("Cleanup of already-removed worktree: %v", err)
	}
}

func TestWorktreeInventoryListsAssignments(t *testing.T) {
	e := newEnv(t)
	a1, a2 := newAction(t), newAction(t)
	w1 := e.assign(t, a1, nil)
	w2 := e.assign(t, a2, nil)

	entries, err := e.svc.WorktreeInventory(context.Background(), e.member)
	if err != nil {
		t.Fatalf("WorktreeInventory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (main + two worktrees)", len(entries))
	}
	byPath := map[string]WorktreeEntry{}
	for _, en := range entries {
		byPath[filepath.Clean(en.Path)] = en
	}
	if en := byPath[filepath.Clean(e.member)]; en.Branch != "main" {
		t.Errorf("main entry branch = %q, want main", en.Branch)
	}
	if en := byPath[filepath.Clean(w1.Path)]; en.Branch != WorktreeBranch(e.workID, a1) {
		t.Errorf("w1 entry branch = %q, want %q", en.Branch, WorktreeBranch(e.workID, a1))
	}
	if en := byPath[filepath.Clean(w2.Path)]; en.Branch != WorktreeBranch(e.workID, a2) {
		t.Errorf("w2 entry branch = %q, want %q", en.Branch, WorktreeBranch(e.workID, a2))
	}

	// Cleanup prunes exactly the explicit worktree, never a sibling.
	if err := e.svc.Cleanup(context.Background(), w1.Worktree, false); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	entries, err = e.svc.WorktreeInventory(context.Background(), e.member)
	if err != nil {
		t.Fatalf("WorktreeInventory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries after cleanup = %d, want 2", len(entries))
	}
	for _, en := range entries {
		if samePath(en.Path, w1.Path) {
			t.Error("cleaned worktree still listed in inventory")
		}
	}
}

// TestCreateAssignmentConcurrentDifferentActions creates worktrees for the
// same member from concurrent goroutines — the -race build proves the
// journal, inventory, and git invocations are safe to run in parallel.
func TestCreateAssignmentConcurrentDifferentActions(t *testing.T) {
	e := newEnv(t)
	const n = 4
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wts := make([]AssignmentWorktree, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			action, err := identity.NewActionID()
			if err != nil {
				errs <- err
				return
			}
			wt, err := e.svc.CreateAssignment(context.Background(), CreateRequest{
				WorkID:        e.workID,
				ActionID:      action,
				RepositoryID:  e.repoID,
				RepositoryDir: e.member,
				BaseCommit:    e.base(t),
				Scope:         []string{"src"},
			})
			if err != nil {
				errs <- err
				return
			}
			wts[i] = wt
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent CreateAssignment: %v", err)
	}

	entries, err := e.svc.WorktreeInventory(context.Background(), e.member)
	if err != nil {
		t.Fatalf("WorktreeInventory: %v", err)
	}
	seen := map[string]bool{}
	for _, wt := range wts {
		if wt.Path == "" {
			continue
		}
		if !registered(t, e.svc, e.member, wt.Path) {
			t.Errorf("worktree %s not registered", wt.Path)
		}
		if seen[wt.Branch] {
			t.Errorf("duplicate branch %s", wt.Branch)
		}
		seen[wt.Branch] = true
	}
	if len(entries) != n+1 {
		t.Errorf("inventory entries = %d, want %d", len(entries), n+1)
	}
	if got := opCount(t, e.db, OpKindCreateWorktree); got != n {
		t.Errorf("create ops = %d, want %d", got, n)
	}
}
