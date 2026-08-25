package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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

// ScopeViolationError names the changed paths that fall outside the scope.
type ScopeViolationError struct {
	Files []string
}

func (e *ScopeViolationError) Error() string {
	return fmt.Sprintf("gitx: changed paths outside declared scope: %s", strings.Join(e.Files, ", "))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ScopeViolationError) Unwrap() error { return ErrScopeViolation }

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

// EmptyCommitMaterialError names the action and commit whose diff against
// the base is empty.
type EmptyCommitMaterialError struct {
	ActionID identity.ActionID
	Commit   string
}

func (e *EmptyCommitMaterialError) Error() string {
	return fmt.Sprintf("gitx: action %s commit %s is empty against its base", e.ActionID, e.Commit)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *EmptyCommitMaterialError) Unwrap() error { return ErrEmptyCommitMaterial }

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

// CommitMaterial is one implementer result: exactly one commit on top of
// the base, with its changed paths, ready to be cherry-picked into an
// integration worktree.
type CommitMaterial struct {
	RepositoryID identity.RepositoryID
	ActionID     identity.ActionID
	BaseCommit   string
	Commit       string
	ChangedPaths []string
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

// CreateRequest names one implementer assignment.
type CreateRequest struct {
	WorkID        identity.WorkID
	ActionID      identity.ActionID
	RepositoryID  identity.RepositoryID
	RepositoryDir string
	BaseCommit    string
	Scope         []string
}

// Validate checks the request in canonical form.
func (r CreateRequest) Validate() error {
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("gitx: work_id: %v", err)
	}
	if err := identity.ValidateUUID(string(r.ActionID)); err != nil {
		return fmt.Errorf("gitx: action_id: %v", err)
	}
	if err := identity.ValidateUUID(string(r.RepositoryID)); err != nil {
		return fmt.Errorf("gitx: repository_id: %v", err)
	}
	if err := validateRepoDir(r.RepositoryDir); err != nil {
		return err
	}
	if r.BaseCommit == "" {
		return fmt.Errorf("gitx: base_commit must not be empty")
	}
	_, err := normalizeScope(r.Scope)
	if err != nil {
		return err
	}
	return nil
}

// IntegrationRequest names one integration and its commit materials.
type IntegrationRequest struct {
	WorkID        identity.WorkID
	RepositoryID  identity.RepositoryID
	RepositoryDir string
	Commits       []CommitMaterial
}

// Validate checks the request in canonical form.
func (r IntegrationRequest) Validate() error {
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("gitx: work_id: %v", err)
	}
	if err := identity.ValidateUUID(string(r.RepositoryID)); err != nil {
		return fmt.Errorf("gitx: repository_id: %v", err)
	}
	if err := validateRepoDir(r.RepositoryDir); err != nil {
		return err
	}
	if len(r.Commits) == 0 {
		return fmt.Errorf("gitx: at least one commit material is required")
	}
	for i, m := range r.Commits {
		if m.RepositoryID != r.RepositoryID {
			return fmt.Errorf("gitx: material %d belongs to repository %s, want %s", i, m.RepositoryID, r.RepositoryID)
		}
		if m.BaseCommit == "" || m.Commit == "" {
			return fmt.Errorf("gitx: material %d must carry a base and a commit", i)
		}
	}
	return nil
}

func validateRepoDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("gitx: repository_dir must not be empty")
	}
	if strings.ContainsRune(dir, '\\') {
		return fmt.Errorf("gitx: repository_dir %q must use '/' as its only separator", dir)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("gitx: repository_dir %q must be absolute", dir)
	}
	if filepath.Clean(dir) != dir {
		return fmt.Errorf("gitx: repository_dir %q must be clean", dir)
	}
	return nil
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

// ValidateResult turns an assignment worktree into a CommitMaterial: the
// worktree must exist, be on its assigned branch, and be clean, hold
// exactly one commit ahead of the base, and that commit's parent must be
// the base with a non-empty diff against it (an empty diff is a typed
// EmptyCommitMaterialError naming the action). Every changed path
// (from diff-tree --name-status, repo-root-relative) must fall inside the
// declared scope — a violation is a typed ScopeViolationError naming the
// offending paths.
func (s *Service) ValidateResult(ctx context.Context, wt AssignmentWorktree, scope []string) (CommitMaterial, error) {
	if _, ok, err := s.worktreeRegistered(ctx, wt.RepoDir, wt.Path); err != nil {
		return CommitMaterial{}, err
	} else if !ok {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: %w", wt.Path, ErrWorktreeMissing)
	}
	cur, err := currentBranch(ctx, s.runner, wt.Path)
	if err != nil {
		return CommitMaterial{}, err
	}
	if cur != wt.Branch {
		return CommitMaterial{}, &BranchMismatchError{Path: wt.Path, Want: wt.Branch, Got: cur}
	}
	files, err := dirtyPaths(ctx, s.runner, wt.Path)
	if err != nil {
		return CommitMaterial{}, err
	}
	if len(files) > 0 {
		return CommitMaterial{}, &DirtyWorktreeError{Files: files}
	}
	out, err := s.runner.Run(ctx, wt.Path, "rev-list", "--count", wt.BaseCommit+"..HEAD")
	if err != nil {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: rev-list: %w", wt.Path, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: rev-list printed %q", wt.Path, out)
	}
	if n != 1 {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: %d commits ahead of base %s, want exactly 1", wt.Path, n, wt.BaseCommit)
	}
	commit, err := revParse(ctx, s.runner, wt.Path, "HEAD", "")
	if err != nil {
		return CommitMaterial{}, err
	}
	parent, err := revParse(ctx, s.runner, wt.Path, "HEAD^", "")
	if err != nil {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: %w", wt.Path, err)
	}
	if parent != wt.BaseCommit {
		return CommitMaterial{}, fmt.Errorf("gitx: validate %s: HEAD commit %s parent %s does not match base %s",
			wt.Path, commit, parent, wt.BaseCommit)
	}
	paths, err := changedPaths(ctx, s.runner, wt.Path, commit)
	if err != nil {
		return CommitMaterial{}, err
	}
	if len(paths) == 0 {
		return CommitMaterial{}, &EmptyCommitMaterialError{ActionID: wt.ActionID, Commit: commit}
	}
	normScope, err := normalizeScope(scope)
	if err != nil {
		return CommitMaterial{}, err
	}
	var violations []string
	for _, p := range paths {
		if !inScope(p, normScope) {
			violations = append(violations, p)
		}
	}
	if len(violations) > 0 {
		return CommitMaterial{}, &ScopeViolationError{Files: violations}
	}
	return CommitMaterial{
		RepositoryID: wt.RepositoryID,
		ActionID:     wt.ActionID,
		BaseCommit:   wt.BaseCommit,
		Commit:       commit,
		ChangedPaths: paths,
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

// WorktreeInventory parses git worktree list --porcelain of repoDir into
// entries (path, HEAD, checked-out branch). It is the recovery inventory:
// orphaned worktrees are pruned only through explicit Cleanup, never
// automatically during active work.
func (s *Service) WorktreeInventory(ctx context.Context, repoDir string) ([]WorktreeEntry, error) {
	out, err := s.runner.Run(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("gitx: worktree list of %s: %w", repoDir, err)
	}
	var entries []WorktreeEntry
	var cur WorktreeEntry
	seen := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			if seen {
				entries = append(entries, cur)
				cur = WorktreeEntry{}
				seen = false
			}
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
			seen = true
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			cur.Branch = ""
		}
	}
	if seen {
		entries = append(entries, cur)
	}
	return entries, nil
}

// worktreeRegistered reports whether path is a registered worktree of
// repoDir and returns its entry.
func (s *Service) worktreeRegistered(ctx context.Context, repoDir, path string) (WorktreeEntry, bool, error) {
	entries, err := s.WorktreeInventory(ctx, repoDir)
	if err != nil {
		return WorktreeEntry{}, false, err
	}
	for _, e := range entries {
		if samePath(e.Path, path) {
			return e, true, nil
		}
	}
	return WorktreeEntry{}, false, nil
}

// samePath compares two paths physically where both exist, falling back to
// lexical equality otherwise (a registered worktree whose directory was
// deleted still matches its path).
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
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

// currentBranch returns the worktree's checked-out branch (short name,
// from git symbolic-ref --short HEAD); empty means detached HEAD.
func currentBranch(ctx context.Context, r Runner, dir string) (string, error) {
	out, err := r.Run(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err == nil {
		return strings.TrimSpace(out), nil
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		return "", nil // detached: symbolic-ref refuses, not a branch
	}
	return "", fmt.Errorf("gitx: symbolic-ref in %s: %w", dir, err)
}

// dirtyPaths returns the repo-root-relative paths with uncommitted changes
// — modified, staged, renamed, or untracked-but-not-ignored — from
// git status --porcelain=v1 -z.
func dirtyPaths(ctx context.Context, r Runner, dir string) ([]string, error) {
	out, err := r.Run(ctx, dir, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: status of %s: %w", dir, err)
	}
	segs := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		if seg == "" {
			continue
		}
		if len(seg) < 4 {
			return nil, fmt.Errorf("gitx: status of %s: unparseable record %q", dir, seg)
		}
		path := seg[3:]
		if path == "" {
			return nil, fmt.Errorf("gitx: status of %s: empty path in record %q", dir, seg)
		}
		paths = append(paths, path)
		switch seg[0] {
		case 'R', 'C':
			i++
			if i >= len(segs) || segs[i] == "" {
				return nil, fmt.Errorf("gitx: status of %s: rename %q without destination", dir, seg)
			}
			paths = append(paths, segs[i])
		}
	}
	return paths, nil
}

// revParse resolves rev (plus suffix) to its full object id, verifying the
// object exists.
func revParse(ctx context.Context, r Runner, dir, rev, suffix string) (string, error) {
	out, err := r.Run(ctx, dir, "rev-parse", "--verify", rev+suffix)
	if err != nil {
		return "", fmt.Errorf("gitx: rev-parse %s%s in %s: %w", rev, suffix, dir, err)
	}
	return strings.TrimSpace(out), nil
}

// refExists reports whether ref exists in the repository at dir.
func refExists(ctx context.Context, r Runner, dir, ref string) (bool, error) {
	_, err := r.Run(ctx, dir, "show-ref", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: show-ref %s in %s: %w", ref, dir, err)
}

// isAncestor reports whether a is an ancestor of b (both commit-ish).
func isAncestor(ctx context.Context, r Runner, dir, a, b string) (bool, error) {
	_, err := r.Run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: merge-base --is-ancestor %s %s in %s: %w", a, b, dir, err)
}

// inCherryPick reports whether a cherry-pick is in progress in dir
// (CHERRY_PICK_HEAD exists).
func inCherryPick(ctx context.Context, r Runner, dir string) bool {
	_, err := r.Run(ctx, dir, "rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD")
	return err == nil
}

// conflictedFiles returns the unmerged paths of a cherry-pick in progress.
func conflictedFiles(ctx context.Context, r Runner, dir string) ([]string, error) {
	out, err := r.Run(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: conflicted files of %s: %w", dir, err)
	}
	var files []string
	for _, seg := range strings.Split(out, "\x00") {
		if seg != "" {
			files = append(files, seg)
		}
	}
	return files, nil
}

// changedPaths returns the repo-root-relative paths a commit changes,
// parsed from git diff-tree --name-status -z (against the commit's first
// parent). Renames contribute both the old and the new path, so a scope
// check can refuse either side of a move.
func changedPaths(ctx context.Context, r Runner, dir, commit string) ([]string, error) {
	out, err := r.Run(ctx, dir, "diff-tree", "-r", "-z", "--no-commit-id", "--name-status", commit)
	if err != nil {
		return nil, fmt.Errorf("gitx: diff-tree of %s in %s: %w", commit, dir, err)
	}
	segs := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		if seg == "" {
			continue
		}
		switch seg[0] {
		case 'A', 'M', 'D', 'T':
			i++
			if i >= len(segs) || segs[i] == "" {
				return nil, fmt.Errorf("gitx: diff-tree of %s: %q without path", commit, seg)
			}
			paths = append(paths, segs[i])
		case 'R', 'C':
			i += 2
			if i >= len(segs) || segs[i-1] == "" || segs[i] == "" {
				return nil, fmt.Errorf("gitx: diff-tree of %s: %q without rename destination", commit, seg)
			}
			paths = append(paths, segs[i-1], segs[i])
		default:
			return nil, fmt.Errorf("gitx: diff-tree of %s: unknown status %q", commit, seg)
		}
	}
	return paths, nil
}

// normalizeScope cleans scope entries into repo-root-relative slash paths:
// entries may name a file or a directory (a directory covers everything
// under it), "." and "" mean the whole repository, and entries that escape
// the root are rejected.
func normalizeScope(scope []string) ([]string, error) {
	out := make([]string, 0, len(scope))
	for _, s := range scope {
		s = strings.TrimSpace(s)
		if s == "" || s == "." {
			out = append(out, "")
			continue
		}
		s = strings.ReplaceAll(s, "\\", "/")
		s = strings.TrimPrefix(s, "./")
		s = strings.TrimSuffix(s, "/")
		if strings.HasPrefix(s, "/") {
			return nil, fmt.Errorf("gitx: scope entry %q must be relative to the repository root", s)
		}
		for _, part := range strings.Split(s, "/") {
			if part == ".." {
				return nil, fmt.Errorf("gitx: scope entry %q escapes the repository root", s)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// inScope reports whether the repo-root-relative path is covered by the
// normalized scope. An empty scope allows the whole repository.
func inScope(rel string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == "" || rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

// ensureParents creates the worktree path's parent directories with
// owner-only permissions, so git never creates them with looser defaults.
// The final component is left for git worktree add to create.
func ensureParents(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("gitx: mkdir %s: %w", parent, err)
	}
	return nil
}

// removeWorktreePath deletes the on-disk worktree directory; missing is
// success.
func removeWorktreePath(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("gitx: remove %s: %w", path, err)
	}
	return nil
}
