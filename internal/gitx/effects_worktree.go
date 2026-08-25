// Worktree create and remove effects: the journal kinds, payloads, and replay.
package gitx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
)

// createAssignmentPayload is the journaled operation payload of a worktree
// creation: the full assignment identity, so recovery and later phases can
// read what was issued.
type createAssignmentPayload struct {
	WorkID       identity.WorkID       `json:"work_id"`
	ActionID     identity.ActionID     `json:"action_id"`
	RepositoryID identity.RepositoryID `json:"repository_id"`
	RepoDir      string                `json:"repo_dir"`
	BaseCommit   string                `json:"base_commit"`
	Scope        []string              `json:"scope"`
	Path         string                `json:"path"`
	Branch       string                `json:"branch"`
}

// integrationPayload is the journaled operation payload of an integration
// worktree creation.
type integrationPayload struct {
	WorkID       identity.WorkID       `json:"work_id"`
	RepositoryID identity.RepositoryID `json:"repository_id"`
	RepoDir      string                `json:"repo_dir"`
	BaseCommit   string                `json:"base_commit"`
	Materials    []CommitMaterial      `json:"materials"`
	Path         string                `json:"path"`
	Branch       string                `json:"branch"`
}

// worktreeCreateOperation is the journaled worktree creation, used for both
// assignments and integrations.
type worktreeCreateOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	payload any
	effects []operation.Effect
}

func (o *worktreeCreateOperation) ID() identity.OperationID    { return o.id }
func (o *worktreeCreateOperation) Kind() string                { return OpKindCreateWorktree }
func (o *worktreeCreateOperation) WorkID() identity.WorkID     { return o.workID }
func (o *worktreeCreateOperation) Generation() int64           { return 0 }
func (o *worktreeCreateOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *worktreeCreateOperation) Payload() any                { return o.payload }
func (o *worktreeCreateOperation) Effects() []operation.Effect { return o.effects }

// worktreeCreatePayload is what the creation effect persists; everything
// replay needs to re-create or remove the worktree.
type worktreeCreatePayload struct {
	RepoDir string `json:"repo_dir"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	// Preexisting records that the worktree was ALREADY there when this
	// operation started, so rolling back must not remove it.
	//
	// An integration area is re-entered by every round after the first.
	// Without this, a failed second round tears down the area the first
	// round built — and then reports the teardown's failure instead of
	// the reason it rolled back.
	Preexisting bool `json:"preexisting,omitempty"`
}

// worktreeCreateEffect creates a linked worktree. Apply is idempotent: a
// registered worktree at the path with the branch is already applied, and a
// branch left behind by a partial apply is attached rather than re-created.
// Revert removes a worktree this operation created (a fresh worktree is
// clean, so the plain removal succeeds).
type worktreeCreateEffect struct {
	runner  Runner
	payload worktreeCreatePayload
}

func (e *worktreeCreateEffect) Kind() string { return kindWorktreeCreate }

func (e *worktreeCreateEffect) Prepare(ctx context.Context) (any, error) {
	// Observed once, at Prepare, and journaled: replay must use what was
	// true when the operation started, not what is true now.
	_, registered, err := worktreeRegisteredWith(ctx, e.runner, e.payload.RepoDir, e.payload.Path)
	if err != nil {
		return nil, err
	}
	p := e.payload
	p.Preexisting = registered
	return p, nil
}

func (e *worktreeCreateEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeWorktreeCreate(rec)
	if err != nil {
		return err
	}
	defer lockWorktree(ctx, e.runner, p.RepoDir)()
	entry, registered, err := worktreeRegisteredWith(ctx, e.runner, p.RepoDir, p.Path)
	if err != nil {
		return err
	}
	if registered {
		if entry.Branch == p.Branch {
			return nil // already applied
		}
		return fmt.Errorf("gitx: worktree %s is registered on branch %s, want %s",
			p.Path, entry.Branch, p.Branch)
	}
	if err := ensureParents(p.Path); err != nil {
		return err
	}
	exists, err := refExists(ctx, e.runner, p.RepoDir, "refs/heads/"+p.Branch)
	if err != nil {
		return err
	}
	if exists {
		_, err = e.runner.Run(ctx, p.RepoDir, "worktree", "add", p.Path, p.Branch)
	} else {
		_, err = e.runner.Run(ctx, p.RepoDir, "worktree", "add", "-b", p.Branch, p.Path, p.Commit)
	}
	if err != nil {
		return fmt.Errorf("gitx: worktree add %s at %s: %w", p.Path, p.Commit, err)
	}
	return nil
}

func (e *worktreeCreateEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeWorktreeCreate(rec)
	if err != nil {
		return err
	}
	defer lockWorktree(ctx, e.runner, p.RepoDir)()
	if p.Preexisting {
		return nil // it was not ours to remove
	}
	_, registered, err := worktreeRegisteredWith(ctx, e.runner, p.RepoDir, p.Path)
	if err != nil {
		return err
	}
	if !registered {
		return nil // nothing to remove
	}
	if _, err := e.runner.Run(ctx, p.RepoDir, "worktree", "remove", p.Path); err != nil {
		return fmt.Errorf("gitx: worktree remove %s: %w", p.Path, err)
	}
	return removeWorktreePath(p.Path)
}

// worktreeRemoveOperation is the journaled cleanup.
type worktreeRemoveOperation struct {
	id      identity.OperationID
	payload any
	effects []operation.Effect
}

func (o *worktreeRemoveOperation) ID() identity.OperationID    { return o.id }
func (o *worktreeRemoveOperation) Kind() string                { return OpKindRemoveWorktree }
func (o *worktreeRemoveOperation) WorkID() identity.WorkID     { return "" }
func (o *worktreeRemoveOperation) Generation() int64           { return 0 }
func (o *worktreeRemoveOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *worktreeRemoveOperation) Payload() any                { return o.payload }
func (o *worktreeRemoveOperation) Effects() []operation.Effect { return o.effects }

// worktreeRemovePayload is what the removal effect persists; Revert needs
// the creation facts to put the worktree back.
type worktreeRemovePayload struct {
	RepoDir string `json:"repo_dir"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Commit  string `json:"commit"`
	Force   bool   `json:"force"`
}

// worktreeRemoveEffect removes a worktree. Apply is idempotent: an
// unregistered (or already removed) path is success, and a removal that
// failed because the tree is dirty surfaces a typed DirtyWorktreeError with
// the dirty paths named unless Force is set. Revert re-creates the worktree
// (roll-back of a cleanup).
type worktreeRemoveEffect struct {
	runner  Runner
	payload worktreeRemovePayload
}

func (e *worktreeRemoveEffect) Kind() string { return kindWorktreeRemove }

func (e *worktreeRemoveEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *worktreeRemoveEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeWorktreeRemove(rec)
	if err != nil {
		return err
	}
	defer lockWorktree(ctx, e.runner, p.RepoDir)()
	_, registered, err := worktreeRegisteredWith(ctx, e.runner, p.RepoDir, p.Path)
	if err != nil {
		return err
	}
	if !registered {
		return nil // missing is success
	}
	args := []string{"worktree", "remove"}
	if p.Force {
		args = append(args, "--force")
	}
	args = append(args, p.Path)
	if _, err := e.runner.Run(ctx, p.RepoDir, args...); err != nil {
		// A refused dirty removal leaves the worktree registered; report
		// the dirty paths. Anything else (a locked worktree, a lost
		// repository) is a loud failure.
		_, stillRegistered, serr := worktreeRegisteredWith(ctx, e.runner, p.RepoDir, p.Path)
		if serr == nil && !stillRegistered {
			return nil // the removal actually happened
		}
		var ce *CommandError
		if errors.As(err, &ce) && strings.Contains(ce.Stderr, "modified or untracked") {
			files, derr := dirtyPaths(ctx, e.runner, p.Path)
			if derr != nil {
				return fmt.Errorf("gitx: worktree remove %s: %w", p.Path, err)
			}
			return &DirtyWorktreeError{Files: files}
		}
		return fmt.Errorf("gitx: worktree remove %s: %w", p.Path, err)
	}
	return removeWorktreePath(p.Path)
}

func (e *worktreeRemoveEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeWorktreeRemove(rec)
	if err != nil {
		return err
	}
	defer lockWorktree(ctx, e.runner, p.RepoDir)()
	if err := ensureParents(p.Path); err != nil {
		return err
	}
	exists, err := refExists(ctx, e.runner, p.RepoDir, "refs/heads/"+p.Branch)
	if err != nil {
		return err
	}
	if exists {
		_, err = e.runner.Run(ctx, p.RepoDir, "worktree", "add", p.Path, p.Branch)
	} else {
		_, err = e.runner.Run(ctx, p.RepoDir, "worktree", "add", "-b", p.Branch, p.Path, p.Commit)
	}
	if err != nil {
		return fmt.Errorf("gitx: worktree add %s at %s (cleanup revert): %w", p.Path, p.Commit, err)
	}
	return nil
}

// decodeWorktreeCreate decodes a worktree-create effect record.
func decodeWorktreeCreate(rec operation.EffectRecord) (worktreeCreatePayload, error) {
	var p worktreeCreatePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, fmt.Errorf("gitx: decode worktree-create payload: %w", err)
	}
	return p, nil
}

func decodeWorktreeRemove(rec operation.EffectRecord) (worktreeRemovePayload, error) {
	var p worktreeRemovePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, fmt.Errorf("gitx: decode worktree-remove payload: %w", err)
	}
	return p, nil
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
