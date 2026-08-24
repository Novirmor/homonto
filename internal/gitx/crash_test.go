package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/store"
)

// TestCreateAssignmentCrashConverges drives the worktree-create operation
// through a crash at every journal boundary and proves recovery converges:
// roll-forward re-applies idempotently (the unrecorded-apply window re-runs
// onto an existing worktree), and a pending-only crash rolls back without
// ever creating the worktree.
func TestCreateAssignmentCrashConverges(t *testing.T) {
	tests := []struct {
		name       string
		point      string
		unrecorded bool
		wantFinal  string
		wantExists bool
	}{
		{"crash after intent row", "pending", false, store.OpRolledBack, false},
		{"crash after prepare", "prepared", false, store.OpFinalized, true},
		{"crash after applied row", "effect-applied", false, store.OpFinalized, true},
		{"crash in unrecorded apply window", "effect-applied-unrecorded:", true, store.OpFinalized, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			action := newAction(t)
			var captured *capturedCrash
			if tc.unrecorded {
				captured = captureUnrecordedCrash(t, tc.point, 1)
			} else {
				setFailpoint(t, tc.point, 1)
			}
			mustCrash(t, func() error {
				_, err := e.svc.CreateAssignment(context.Background(), CreateRequest{
					WorkID:        e.workID,
					ActionID:      action,
					RepositoryID:  e.repoID,
					RepositoryDir: e.member,
					BaseCommit:    e.base(t),
				})
				return err
			})
			if captured != nil {
				captured.restore()
				if state := opState(t, e.db, captured.id); state != store.OpPrepared {
					t.Fatalf("crashed op %s state = %q, want prepared", captured.id, state)
				}
			}

			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}

			id, state := latestOp(t, e.db, OpKindCreateWorktree)
			if state != tc.wantFinal {
				t.Errorf("op %s state = %q, want %q", id, state, tc.wantFinal)
			}
			path := WorktreePath(e.root, e.repoID, action)
			if tc.wantExists {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("worktree %s missing after recovery: %v", path, err)
				}
				if !registered(t, e.svc, e.member, path) {
					t.Errorf("worktree %s not registered after recovery", path)
				}
			} else if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("worktree %s exists after rolled-back recovery (err %v)", path, err)
			}
		})
	}
}

// TestApplyCommitCrashConverges crashes the journaled cherry-pick at every
// boundary and proves roll-forward recovery finishes the pick: the
// unrecorded-apply window re-applies onto the already-picked branch (the
// ancestor check makes it a no-op) and the commit lands in the integration
// worktree.
func TestApplyCommitCrashConverges(t *testing.T) {
	tests := []struct {
		name       string
		point      string
		unrecorded bool
		wantFinal  string
	}{
		{"crash after prepare", "prepared", false, store.OpFinalized},
		{"crash after applied row", "effect-applied", false, store.OpFinalized},
		{"crash in unrecorded apply window", "effect-applied-unrecorded:", true, store.OpFinalized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			wt := e.assign(t, newAction(t), []string{"src"})
			implement(t, wt, "src/a.go", "package a\n", "implement a")
			m := material(t, e, wt, []string{"src"})
			iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
				WorkID:        e.workID,
				RepositoryID:  e.repoID,
				RepositoryDir: e.member,
				Commits:       []CommitMaterial{m},
			})
			if err != nil {
				t.Fatalf("CreateIntegration: %v", err)
			}

			var captured *capturedCrash
			if tc.unrecorded {
				captured = captureUnrecordedCrash(t, tc.point, 1)
			} else {
				setFailpoint(t, tc.point, 1)
			}
			mustCrash(t, func() error {
				_, err := e.svc.ApplyCommit(context.Background(), iwt, m)
				return err
			})
			if captured != nil {
				captured.restore()
				if state := opState(t, e.db, captured.id); state != store.OpPrepared {
					t.Fatalf("crashed op %s state = %q, want prepared", captured.id, state)
				}
			}

			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}

			id, state := latestOp(t, e.db, OpKindCherryPick)
			if state != tc.wantFinal {
				t.Errorf("op %s state = %q, want %q", id, state, tc.wantFinal)
			}
			data, err := os.ReadFile(filepath.Join(iwt.Path, "src", "a.go"))
			if err != nil {
				t.Fatalf("picked file missing after recovery: %v", err)
			}
			if string(data) != "package a\n" {
				t.Errorf("picked file = %q, want %q", data, "package a\n")
			}
		})
	}
}

// TestCleanupCrashConverges crashes the worktree-remove operation at every
// journal boundary: only the pending-only crash leaves the worktree in
// place (recovery aborts pending operations without running effects);
// everything else converges to a fully cleaned worktree.
func TestCleanupCrashConverges(t *testing.T) {
	tests := []struct {
		name       string
		point      string
		unrecorded bool
		wantFinal  string
		wantExists bool
	}{
		{"crash after intent row", "pending", false, store.OpRolledBack, true},
		{"crash after prepare", "prepared", false, store.OpFinalized, false},
		{"crash after applied row", "effect-applied", false, store.OpFinalized, false},
		{"crash in unrecorded apply window", "effect-applied-unrecorded:", true, store.OpFinalized, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			wt := e.assign(t, newAction(t), nil)

			var captured *capturedCrash
			if tc.unrecorded {
				captured = captureUnrecordedCrash(t, tc.point, 1)
			} else {
				setFailpoint(t, tc.point, 1)
			}
			mustCrash(t, func() error {
				return e.svc.Cleanup(context.Background(), wt.Worktree, false)
			})
			if captured != nil {
				captured.restore()
				if state := opState(t, e.db, captured.id); state != store.OpPrepared {
					t.Fatalf("crashed op %s state = %q, want prepared", captured.id, state)
				}
			}

			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}

			id, state := latestOp(t, e.db, OpKindRemoveWorktree)
			if state != tc.wantFinal {
				t.Errorf("op %s state = %q, want %q", id, state, tc.wantFinal)
			}
			if tc.wantExists {
				if _, err := os.Stat(wt.Path); err != nil {
					t.Errorf("worktree %s missing after rolled-back recovery: %v", wt.Path, err)
				}
			} else {
				if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
					t.Errorf("worktree %s exists after recovery (err %v)", wt.Path, err)
				}
				if registered(t, e.svc, e.member, wt.Path) {
					t.Error("worktree still registered after recovery")
				}
			}
		})
	}
}

// TestAbortIntegrationCrashConverges crashes the two-effect abort operation
// between effects and proves roll-forward recovery completes both: the
// cherry-pick is aborted and the worktree is removed.
func TestAbortIntegrationCrashConverges(t *testing.T) {
	tests := []struct {
		name      string
		point     string
		nth       int
		wantFinal string
	}{
		{"crash after prepare", "prepared", 1, store.OpFinalized},
		{"crash after abort row, before remove", "effect-applied", 1, store.OpFinalized},
		{"crash after both rows", "effect-applied", 2, store.OpFinalized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			w1 := e.assign(t, newAction(t), []string{"src"})
			implement(t, w1, "src/shared.txt", "one\n", "one")
			m1 := material(t, e, w1, []string{"src"})
			w2 := e.assign(t, newAction(t), []string{"src"})
			implement(t, w2, "src/shared.txt", "two\n", "two")
			m2 := material(t, e, w2, []string{"src"})
			iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
				WorkID:        e.workID,
				RepositoryID:  e.repoID,
				RepositoryDir: e.member,
				Commits:       []CommitMaterial{m1, m2},
			})
			if err != nil {
				t.Fatalf("CreateIntegration: %v", err)
			}
			if _, err := e.svc.ApplyCommit(context.Background(), iwt, m1); err != nil {
				t.Fatalf("ApplyCommit m1: %v", err)
			}
			if _, err := e.svc.ApplyCommit(context.Background(), iwt, m2); !isConflict(err) {
				t.Fatalf("ApplyCommit m2 = %v, want conflict", err)
			}

			setFailpoint(t, tc.point, tc.nth)
			mustCrash(t, func() error {
				return e.svc.AbortIntegration(context.Background(), iwt)
			})

			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("recover: %v", err)
			}

			id, state := latestOp(t, e.db, OpKindCherryPickAbort)
			if state != tc.wantFinal {
				t.Errorf("op %s state = %q, want %q", id, state, tc.wantFinal)
			}
			if _, err := os.Stat(iwt.Path); !os.IsNotExist(err) {
				t.Errorf("integration path %s exists after recovery (err %v)", iwt.Path, err)
			}
			if registered(t, e.svc, e.member, iwt.Path) {
				t.Error("integration worktree still registered after recovery")
			}
		})
	}
}

// isConflict reports whether err is (or wraps) a cherry-pick conflict.
func isConflict(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
}

// conflictFixture builds an integration whose second ApplyCommit conflicts:
// two materials touching src/shared.txt with different content, the first
// applied cleanly. It returns the env, the integration worktree, and the
// conflicting material.
func conflictFixture(t *testing.T) (*env, IntegrationWorktree, CommitMaterial) {
	t.Helper()
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/shared.txt", "one\n", "one")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/shared.txt", "two\n", "two")
	m2 := material(t, e, w2, []string{"src"})
	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1, m2},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, m1); err != nil {
		t.Fatalf("ApplyCommit m1: %v", err)
	}
	return e, iwt, m2
}

// commitsAhead counts commits on HEAD not reachable from base.
func commitsAhead(t *testing.T, dir, base string) int {
	t.Helper()
	out, err := ExecRunner{}.Run(context.Background(), dir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		t.Fatalf("gitx: rev-list in %s: %v", dir, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("gitx: rev-list printed %q", out)
	}
	return n
}

// pickInProgress reports whether a cherry-pick is in progress in dir.
func pickInProgress(t *testing.T, dir string) bool {
	t.Helper()
	_, err := ExecRunner{}.Run(context.Background(), dir, "rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD")
	return err == nil
}

// resolveConflict stages an engine-style resolution of src/shared.txt.
func resolveConflict(t *testing.T, dir, content string) {
	t.Helper()
	shared := filepath.Join(dir, "src", "shared.txt")
	if err := os.WriteFile(shared, []byte(content), 0o644); err != nil {
		t.Fatalf("gitx: write resolution: %v", err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), dir, "add", "src/shared.txt"); err != nil {
		t.Fatalf("gitx: stage resolution: %v", err)
	}
}

// TestApplyCommitConflictCrashConverges crashes the conflicted cherry-pick
// inside the apply-error window — after Apply returned ConflictError but
// before the failed row and the roll-back policy are both durable — and
// proves the invariant: RecoverPending never re-runs the pick, so the
// conflicted stop is preserved for the engine (or its completed resolution
// is left alone) and the integration branch never gains a duplicate commit.
func TestApplyCommitConflictCrashConverges(t *testing.T) {
	t.Run("crash after conflict before failed row", func(t *testing.T) {
		e, iwt, m2 := conflictFixture(t)
		captured := captureUnrecordedCrash(t, "effect-failed-unrecorded:", 1)
		mustCrash(t, func() error {
			_, err := e.svc.ApplyCommit(context.Background(), iwt, m2)
			return err
		})
		captured.restore()
		if state := opState(t, e.db, captured.id); state != store.OpPrepared {
			t.Fatalf("crashed op %s state = %q, want prepared", captured.id, state)
		}

		e.close(t)
		e.open(t)
		if err := e.svc.Recover(context.Background()); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if id, state := latestOp(t, e.db, OpKindCherryPick); state != store.OpFinalized {
			t.Errorf("conflicted pick op %s state = %q, want finalized (re-apply recognized its own conflicted stop)", id, state)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 1 {
			t.Errorf("commits ahead of base after recovery = %d, want 1 (m1 only; conflicted pick not re-run)", got)
		}
		if !pickInProgress(t, iwt.Path) {
			t.Error("cherry-pick no longer in progress after recovery; the conflicted stop must survive for the engine")
		}

		// The engine flow still completes: resolve and continue, landing
		// exactly one commit for the material.
		resolveConflict(t, iwt.Path, "resolved\n")
		if err := e.svc.ContinueConflict(context.Background(), iwt); err != nil {
			t.Fatalf("ContinueConflict after recovery: %v", err)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 2 {
			t.Errorf("commits ahead of base after continue = %d, want 2 (m1 + resolution; no duplicate pick)", got)
		}
	})

	t.Run("crash after conflict, pick completed out of band", func(t *testing.T) {
		e, iwt, m2 := conflictFixture(t)
		captured := captureUnrecordedCrash(t, "effect-failed-unrecorded:", 1)
		mustCrash(t, func() error {
			_, err := e.svc.ApplyCommit(context.Background(), iwt, m2)
			return err
		})
		captured.restore()

		// The engine finishes the pick while the crashed operation still
		// holds a pending row (ContinueConflict is a plain
		// cherry-pick --continue): a resolution that differs from the
		// material's tree.
		resolveConflict(t, iwt.Path, "merged by hand\n")
		if _, err := (ExecRunner{}).Run(context.Background(), iwt.Path, "cherry-pick", "--continue"); err != nil {
			t.Fatalf("gitx: out-of-band continue: %v", err)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 2 {
			t.Fatalf("commits ahead after continue = %d, want 2", got)
		}

		e.close(t)
		e.open(t)
		if err := e.svc.Recover(context.Background()); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if id, state := latestOp(t, e.db, OpKindCherryPick); state != store.OpFinalized {
			t.Errorf("conflicted pick op %s state = %q, want finalized", id, state)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 2 {
			t.Errorf("commits ahead of base after recovery = %d, want 2 (recovery must not re-apply the completed pick)", got)
		}
		if pickInProgress(t, iwt.Path) {
			t.Error("cherry-pick in progress after recovery of a completed pick")
		}
	})

	t.Run("crash after failed row before policy switch", func(t *testing.T) {
		e, iwt, m2 := conflictFixture(t)
		setFailpoint(t, "effect-failed", 1)
		mustCrash(t, func() error {
			_, err := e.svc.ApplyCommit(context.Background(), iwt, m2)
			return err
		})

		e.close(t)
		e.open(t)
		if err := e.svc.Recover(context.Background()); err != nil {
			t.Fatalf("recover: %v", err)
		}
		if id, state := latestOp(t, e.db, OpKindCherryPick); state != store.OpRolledBack {
			t.Errorf("conflicted pick op %s state = %q, want rolled back (failed row switches recovery to roll-back)", id, state)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 1 {
			t.Errorf("commits ahead of base after recovery = %d, want 1 (m1 only; failed row never re-applied)", got)
		}
		if !pickInProgress(t, iwt.Path) {
			t.Error("cherry-pick no longer in progress after recovery; the conflicted stop must survive for the engine")
		}

		// The engine flow still completes on the rolled-back journal.
		resolveConflict(t, iwt.Path, "resolved\n")
		if err := e.svc.ContinueConflict(context.Background(), iwt); err != nil {
			t.Fatalf("ContinueConflict after recovery: %v", err)
		}
		if got := commitsAhead(t, iwt.Path, iwt.BaseCommit); got != 2 {
			t.Errorf("commits ahead of base after continue = %d, want 2", got)
		}
	})
}
