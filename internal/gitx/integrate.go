package gitx

import (
	"context"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
)

// CreateIntegration creates the integration worktree for one member: the
// integration branch homonto/integration/<work-id>/<repository-id> at the
// shared base commit of the first material, under
// .homonto/integrations/<work-id>/<repository-id>/git. All materials must
// share one base commit and one repository — otherwise a typed
// ErrBaseMismatch (or a validation error) is returned. Applying the
// materials is the engine's job: it calls ApplyCommit per material in
// dependency order (then action-id order), resolving any conflict through
// ContinueConflict or abandoning through AbortIntegration.
func (s *Service) CreateIntegration(ctx context.Context, req IntegrationRequest) (IntegrationWorktree, error) {
	if err := req.Validate(); err != nil {
		return IntegrationWorktree{}, err
	}
	base := req.Commits[0].BaseCommit
	for i, m := range req.Commits[1:] {
		if m.BaseCommit != base {
			return IntegrationWorktree{}, fmt.Errorf("gitx: material %d base %s does not match %s: %w",
				i+1, m.BaseCommit, base, ErrBaseMismatch)
		}
	}
	for _, m := range req.Commits {
		if _, err := revParse(ctx, s.runner, req.RepositoryDir, m.Commit, "^{commit}"); err != nil {
			return IntegrationWorktree{}, err
		}
	}

	path := IntegrationPath(s.root, req.WorkID, req.RepositoryID)
	branch := IntegrationBranch(req.WorkID, req.RepositoryID)
	opID, err := identity.NewOperationID()
	if err != nil {
		return IntegrationWorktree{}, fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &worktreeCreateOperation{
		id:     opID,
		workID: req.WorkID,
		payload: integrationPayload{
			WorkID:       req.WorkID,
			RepositoryID: req.RepositoryID,
			RepoDir:      req.RepositoryDir,
			BaseCommit:   base,
			Materials:    req.Commits,
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
			return IntegrationWorktree{}, fmt.Errorf("gitx: create integration %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return IntegrationWorktree{}, fmt.Errorf("gitx: create integration %s: %w", opID, err)
	}
	return IntegrationWorktree{
		Worktree:     Worktree{Path: path, Branch: branch, BaseCommit: base, RepoDir: req.RepositoryDir},
		WorkID:       req.WorkID,
		RepositoryID: req.RepositoryID,
	}, nil
}

// ApplyCommit cherry-picks one commit material into an integration
// worktree, journaled as one operation. A clean pick returns Applied; a
// conflict leaves the cherry-pick in progress — the worktree stays
// mid-conflict, unmerged paths staged by git — and returns a typed
// ConflictError naming the conflicted files, with the operation switched to
// roll-back so recovery never re-applies it. The engine then orchestrates
// resolution (ContinueConflict) or abandonment (AbortIntegration).
func (s *Service) ApplyCommit(ctx context.Context, dst IntegrationWorktree, src CommitMaterial) (ApplyResult, error) {
	if src.BaseCommit != dst.BaseCommit {
		return ApplyResult{}, fmt.Errorf("gitx: apply commit %s: base %s does not match integration base %s",
			src.Commit, src.BaseCommit, dst.BaseCommit)
	}
	if _, ok, err := s.worktreeRegistered(ctx, dst.RepoDir, dst.Path); err != nil {
		return ApplyResult{}, err
	} else if !ok {
		return ApplyResult{}, fmt.Errorf("gitx: apply commit: %w", ErrWorktreeMissing)
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		return ApplyResult{}, fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &cherryPickOperation{
		id:     opID,
		workID: dst.WorkID,
		payload: map[string]any{
			"dir": dst.Path, "commit": src.Commit, "action_id": src.ActionID,
		},
		effects: []operation.Effect{&cherryPickEffect{
			runner:  s.runner,
			payload: cherryPickPayload{Dir: dst.Path, Commit: src.Commit},
		}},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		cleanupErr := s.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return ApplyResult{}, fmt.Errorf("gitx: apply commit %s: %v (cleanup: %v)", src.Commit, err, cleanupErr)
		}
		var ce *ConflictError
		if errors.As(err, &ce) {
			return ApplyResult{Conflicts: ce.Files}, err
		}
		return ApplyResult{}, err
	}
	return ApplyResult{Applied: true}, nil
}

// ContinueConflict finishes an in-progress cherry-pick after the engine
// resolved the conflicts in the worktree and staged them. The finish is
// journaled and idempotent: no cherry-pick in progress is success, so an
// interrupted continue rolls forward to the same terminal state.
func (s *Service) ContinueConflict(ctx context.Context, dst IntegrationWorktree) error {
	if _, ok, err := s.worktreeRegistered(ctx, dst.RepoDir, dst.Path); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("gitx: continue conflict: %w", ErrWorktreeMissing)
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &cherryPickContinueOperation{
		id:     opID,
		workID: dst.WorkID,
		payload: map[string]any{
			"dir": dst.Path,
		},
		effects: []operation.Effect{&cherryPickContinueEffect{
			runner:  s.runner,
			payload: cherryPickContinuePayload{Dir: dst.Path},
		}},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		cleanupErr := s.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return fmt.Errorf("gitx: continue conflict in %s: %v (cleanup: %v)", dst.Path, err, cleanupErr)
		}
		return fmt.Errorf("gitx: continue conflict in %s: %w", dst.Path, err)
	}
	return nil
}

// AbortIntegration abandons an integration: it aborts the in-progress
// cherry-pick and removes the integration worktree, journaled as one
// operation so an interrupted abort rolls forward to the same clean state
// on recovery. The integration branch ref is left in place — integration
// branches are the durable handoff and are never deleted by Homonto.
func (s *Service) AbortIntegration(ctx context.Context, dst IntegrationWorktree) error {
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("gitx: generate operation id: %w", err)
	}
	op := &cherryPickAbortOperation{
		id:     opID,
		workID: dst.WorkID,
		payload: map[string]any{
			"dir": dst.Path, "branch": dst.Branch,
		},
		effects: []operation.Effect{
			&cherryPickAbortEffect{
				runner:  s.runner,
				payload: cherryPickAbortPayload{Dir: dst.Path},
			},
			&worktreeRemoveEffect{
				runner: s.runner,
				payload: worktreeRemovePayload{
					RepoDir: dst.RepoDir, Path: dst.Path, Branch: dst.Branch, Commit: dst.BaseCommit,
				},
			},
		},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		cleanupErr := s.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return fmt.Errorf("gitx: abort integration %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return fmt.Errorf("gitx: abort integration %s: %w", opID, err)
	}
	return nil
}
