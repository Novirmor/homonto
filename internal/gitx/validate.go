// Validation of requests and results: materials, scope, and typed refusals.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
)

// ScopeViolationError names the changed paths that fall outside the scope.
type ScopeViolationError struct {
	Files []string
}

func (e *ScopeViolationError) Error() string {
	return fmt.Sprintf("gitx: changed paths outside declared scope: %s", strings.Join(e.Files, ", "))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ScopeViolationError) Unwrap() error { return ErrScopeViolation }

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
