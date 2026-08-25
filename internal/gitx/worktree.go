package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// Operation kinds journaled by this package.
const (
	// OpKindCreateWorktree names a worktree creation (one per assignment
	// and one per integration).
	OpKindCreateWorktree = "gitx.worktree.create"
	// OpKindRemoveWorktree names a cleanup.
	OpKindRemoveWorktree = "gitx.worktree.remove"
	// OpKindCherryPick names one applied commit material.
	OpKindCherryPick = "gitx.cherry-pick"
	// OpKindCherryPickContinue names a finished conflict resolution.
	OpKindCherryPickContinue = "gitx.cherry-pick.continue"
	// OpKindCherryPickAbort names an abandoned integration.
	OpKindCherryPickAbort = "gitx.cherry-pick.abort"
)

// Effect kinds registered for recovery.
const (
	kindWorktreeCreate     = "gitx.worktree-create"
	kindWorktreeRemove     = "gitx.worktree-remove"
	kindCherryPick         = "gitx.cherry-pick"
	kindCherryPickContinue = "gitx.cherry-pick-continue"
	kindCherryPickAbort    = "gitx.cherry-pick-abort"
	kindIntegrationReset   = "gitx.integration-reset"
)

// Typed errors. Wrap with context via fmt.Errorf("%w", ...) so callers can
// branch with errors.Is and read details with errors.As.
var (
	// ErrDirtyWorktree: the member or worktree has uncommitted changes and
	// the operation refuses to proceed (ADR 0024: dirty worktrees are
	// rejected, never tidied).
	ErrDirtyWorktree = errors.New("gitx: worktree is dirty")
	// ErrScopeViolation: a commit changes paths outside its declared scope.
	ErrScopeViolation = errors.New("gitx: changed paths outside declared scope")
	// ErrBaseMismatch: integration materials do not share one base commit.
	ErrBaseMismatch = errors.New("gitx: integration materials must share one base commit")
	// ErrConflict: a cherry-pick stopped on conflicts.
	ErrConflict = errors.New("gitx: cherry-pick conflict")
	// ErrWorktreeMissing: the named worktree is not registered.
	ErrWorktreeMissing = errors.New("gitx: worktree not found")
	// ErrEmptyCommitMaterial: a validated result commit carries no changes
	// against its base; cherry-picking it would stop empty and deadlock
	// conflict continuation.
	ErrEmptyCommitMaterial = errors.New("gitx: commit material is empty")
	// ErrBranchMismatch: the worktree is not on its assigned branch.
	ErrBranchMismatch = errors.New("gitx: worktree branch mismatch")
)

// DirtyWorktreeError names the paths that make a tree dirty.
type DirtyWorktreeError struct {
	Files []string
}

func (e *DirtyWorktreeError) Error() string {
	return fmt.Sprintf("gitx: worktree is dirty: %s", strings.Join(e.Files, ", "))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *DirtyWorktreeError) Unwrap() error { return ErrDirtyWorktree }

// ConflictError names the files a cherry-pick stopped on. Homonto never
// auto-resolves: the worktree is left mid-conflict for the engine to
// orchestrate resolution, then ContinueConflict or AbortIntegration.
type ConflictError struct {
	Files []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("gitx: cherry-pick conflict on %s", strings.Join(e.Files, ", "))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ConflictError) Unwrap() error { return ErrConflict }

// BranchMismatchError names the branch a worktree should be on and the one
// it is on (empty when detached).
type BranchMismatchError struct {
	Path string
	Want string
	Got  string
}

func (e *BranchMismatchError) Error() string {
	if e.Got == "" {
		return fmt.Sprintf("gitx: worktree %s is detached, want branch %s", e.Path, e.Want)
	}
	return fmt.Sprintf("gitx: worktree %s is on branch %s, want %s", e.Path, e.Got, e.Want)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *BranchMismatchError) Unwrap() error { return ErrBranchMismatch }

// Worktree describes one registered git worktree.
type Worktree struct {
	// Path is the worktree's working-tree root.
	Path string
	// Branch is the branch the worktree has checked out.
	Branch string
	// BaseCommit is the commit the worktree was created from.
	BaseCommit string
	// RepoDir is the member repository the worktree belongs to; every git
	// command about the worktree runs there.
	RepoDir string
}

// AssignmentWorktree is an implementer's isolated worktree.
type AssignmentWorktree struct {
	Worktree
	WorkID       identity.WorkID
	ActionID     identity.ActionID
	RepositoryID identity.RepositoryID
}

// IntegrationWorktree is the worktree commit materials are cherry-picked
// into.
type IntegrationWorktree struct {
	Worktree
	WorkID       identity.WorkID
	RepositoryID identity.RepositoryID
}

// ApplyResult reports one ApplyCommit outcome.
type ApplyResult struct {
	// Applied is true when the commit was cherry-picked without conflicts.
	Applied bool
	// Conflicts lists the conflicted paths when Applied is false; the
	// cherry-pick is left in progress for the engine to resolve.
	Conflicts []string
}

// WorktreeEntry is one entry of git worktree list --porcelain.
type WorktreeEntry struct {
	Path   string
	Head   string
	Branch string // short branch name; empty when detached
}

// WorktreePath derives the assignment worktree path for one action:
// <root>/.homonto/worktrees/<repository-id>/<action-id>.
func WorktreePath(root string, repoID identity.RepositoryID, actionID identity.ActionID) string {
	return filepath.Join(root, ".homonto", "worktrees", string(repoID), string(actionID))
}

// WorktreeBranch derives the assignment branch name:
// homonto/work/<work-id>/<action-id>.
func WorktreeBranch(workID identity.WorkID, actionID identity.ActionID) string {
	return "homonto/work/" + string(workID) + "/" + string(actionID)
}

// IntegrationPath derives the integration worktree path for one member:
// <root>/.homonto/integrations/<work-id>/<repository-id>/git.
func IntegrationPath(root string, workID identity.WorkID, repoID identity.RepositoryID) string {
	return filepath.Join(root, ".homonto", "integrations", string(workID), string(repoID), "git")
}

// IntegrationBranch derives the integration branch name:
// homonto/integration/<work-id>/<repository-id>.
func IntegrationBranch(workID identity.WorkID, repoID identity.RepositoryID) string {
	return "homonto/integration/" + string(workID) + "/" + string(repoID)
}

// Service provides the workflow's git plumbing. It is safe for concurrent
// use: the store serializes journal transitions and git subprocesses are
// independent.
type Service struct {
	runner Runner
	db     *store.DB
	ops    *operation.Manager
	root   string
}

// NewService returns a Service journaling through db and ops and placing
// worktrees under root/.homonto/worktrees and root/.homonto/integrations.
// The git effect kinds are registered immediately so both in-process
// cleanup and recovery can dispatch them.
func NewService(runner Runner, db *store.DB, ops *operation.Manager, root string) (*Service, error) {
	if db == nil || ops == nil {
		return nil, fmt.Errorf("gitx: db and operation managers are required")
	}
	if err := validateRepoDir(root); err != nil {
		return nil, fmt.Errorf("gitx: root: %w", err)
	}
	if runner == nil {
		runner = ExecRunner{}
	}
	s := &Service{runner: runner, db: db, ops: ops, root: root}
	s.registerEffects()
	return s, nil
}

// registerEffects installs every git effect prototype for recovery
// dispatch. Registration is idempotent.
func (s *Service) registerEffects() {
	s.ops.RegisterEffect(&worktreeCreateEffect{runner: s.runner})
	s.ops.RegisterEffect(&worktreeRemoveEffect{runner: s.runner})
	s.ops.RegisterEffect(&cherryPickEffect{runner: s.runner})
	s.ops.RegisterEffect(&cherryPickContinueEffect{runner: s.runner})
	s.ops.RegisterEffect(&cherryPickAbortEffect{runner: s.runner})
	s.ops.RegisterEffect(&integrationResetEffect{runner: s.runner})
}

// CreateAssignment creates an isolated implementer worktree for one action.
//
// The member repository must be clean at its HEAD (ADR 0024: dirty trees
// are rejected, never tidied); the dirty paths are named in the typed
// DirtyWorktreeError. The worktree is then created from BaseCommit under
// .homonto/worktrees/<repository-id>/<action-id> on branch
// homonto/work/<work-id>/<action-id>, journaled as one operation so an
// interrupted creation rolls forward on recovery. The member's main working
// tree is never touched.
func (s *Service) CreateAssignment(ctx context.Context, req CreateRequest) (AssignmentWorktree, error) {
	if err := req.Validate(); err != nil {
		return AssignmentWorktree{}, err
	}
	// ADR 0024: reject dirty at work start, before anything is journaled.
	files, err := dirtyPaths(ctx, s.runner, req.RepositoryDir)
	if err != nil {
		return AssignmentWorktree{}, err
	}
	if len(files) > 0 {
		return AssignmentWorktree{}, &DirtyWorktreeError{Files: files}
	}
	base, err := revParse(ctx, s.runner, req.RepositoryDir, req.BaseCommit, "^{commit}")
	if err != nil {
		return AssignmentWorktree{}, err
	}

	path := WorktreePath(s.root, req.RepositoryID, req.ActionID)
	branch := WorktreeBranch(req.WorkID, req.ActionID)
	opID, err := identity.NewOperationID()
	if err != nil {
		return AssignmentWorktree{}, fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &worktreeCreateOperation{
		id:     opID,
		workID: req.WorkID,
		payload: createAssignmentPayload{
			WorkID:       req.WorkID,
			ActionID:     req.ActionID,
			RepositoryID: req.RepositoryID,
			RepoDir:      req.RepositoryDir,
			BaseCommit:   base,
			Scope:        req.Scope,
			Path:         path,
			Branch:       branch,
		},
		effects: []operation.Effect{&worktreeCreateEffect{
			runner: s.runner,
			payload: worktreeCreatePayload{
				RepoDir: req.RepositoryDir, Path: path, Branch: branch, Commit: base,
			},
		}},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		cleanupErr := s.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return AssignmentWorktree{}, fmt.Errorf("gitx: create assignment %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return AssignmentWorktree{}, fmt.Errorf("gitx: create assignment %s: %w", opID, err)
	}
	return AssignmentWorktree{
		Worktree:     Worktree{Path: path, Branch: branch, BaseCommit: base, RepoDir: req.RepositoryDir},
		WorkID:       req.WorkID,
		ActionID:     req.ActionID,
		RepositoryID: req.RepositoryID,
	}, nil
}

// Cleanup removes a worktree (assignment or integration) and its on-disk
// path. Removal is plain: a dirty worktree is refused with the dirty paths
// named unless force is set. The operation is journaled and idempotent — a
// missing worktree is success — and an interrupted cleanup rolls forward on
// recovery. The branch ref is left in place: integration branches are the
// durable handoff and are never deleted by cleanup.
func (s *Service) Cleanup(ctx context.Context, wt Worktree, force bool) error {
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &worktreeRemoveOperation{
		id: opID,
		effects: []operation.Effect{&worktreeRemoveEffect{
			runner: s.runner,
			payload: worktreeRemovePayload{
				RepoDir: wt.RepoDir, Path: wt.Path, Branch: wt.Branch, Commit: wt.BaseCommit, Force: force,
			},
		}},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		cleanupErr := s.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return fmt.Errorf("gitx: cleanup %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return fmt.Errorf("gitx: cleanup %s: %w", opID, err)
	}
	return nil
}

// finishOrRollBack drives one journaled git operation to a terminal state
// after an in-process failure. Git side effects are never rolled forward
// after a live error: the operation is switched to roll-back so recovery
// re-runs the idempotent reverts instead of re-applying a failed side
// effect.
func (s *Service) finishOrRollBack(ctx context.Context, opID identity.OperationID) error {
	rec, err := s.db.Operation(ctx, opID)
	if err != nil {
		return fmt.Errorf("gitx: cleanup: %w", err)
	}
	switch rec.State {
	case store.OpFinalized, store.OpRolledBack:
		return nil
	}
	if err := s.db.Update(ctx, func(tx *store.Tx) error {
		return tx.SetOperationPolicy(ctx, opID, string(operation.RollBack))
	}); err != nil {
		return fmt.Errorf("gitx: cleanup: switch %s to roll-back: %w", opID, err)
	}
	return s.ops.RecoverOne(ctx, opID)
}
