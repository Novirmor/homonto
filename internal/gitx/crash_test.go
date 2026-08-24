package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
