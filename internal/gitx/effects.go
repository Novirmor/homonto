package gitx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func (e *worktreeCreateEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *worktreeCreateEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeWorktreeCreate(rec)
	if err != nil {
		return err
	}
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

// cherryPickOperation is the journaled application of one commit material.
type cherryPickOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	payload any
	effects []operation.Effect
}

func (o *cherryPickOperation) ID() identity.OperationID    { return o.id }
func (o *cherryPickOperation) Kind() string                { return OpKindCherryPick }
func (o *cherryPickOperation) WorkID() identity.WorkID     { return o.workID }
func (o *cherryPickOperation) Generation() int64           { return 0 }
func (o *cherryPickOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *cherryPickOperation) Payload() any                { return o.payload }
func (o *cherryPickOperation) Effects() []operation.Effect { return o.effects }

// cherryPickPayload is what the pick effect persists. Before is HEAD when
// the pick prepared — what Revert restores.
type cherryPickPayload struct {
	Dir    string `json:"dir"`
	Commit string `json:"commit"`
	Before string `json:"before"`
}

// cherryPickEffect cherry-picks one commit into a worktree. Apply is
// idempotent: an already-applied commit (an ancestor of HEAD, or any commit
// whose first parent is the journaled pre-pick HEAD) is success, and so is
// this pick's own conflicted stop — a CHERRY_PICK_HEAD naming the journaled
// commit means the pick already ran and stopped on the conflict for the
// engine to resolve. A conflict leaves the cherry-pick in progress and
// returns a typed ConflictError naming the conflicted paths; Run journals
// the effect failed and switches the operation to roll-back before
// returning, so recovery never re-applies a conflicted pick. Revert undoes
// an applied pick by resetting back to Before.
type cherryPickEffect struct {
	runner  Runner
	payload cherryPickPayload
}

func (e *cherryPickEffect) Kind() string { return kindCherryPick }

func (e *cherryPickEffect) Prepare(ctx context.Context) (any, error) {
	before, err := revParse(ctx, e.runner, e.payload.Dir, "HEAD", "")
	if err != nil {
		return nil, err
	}
	p := e.payload
	p.Before = before
	return p, nil
}

func (e *cherryPickEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeCherryPick(rec)
	if err != nil {
		return err
	}
	// Idempotency: the pick is already applied when the original commit is
	// an ancestor of HEAD, or when HEAD already holds the pick's outcome —
	// any commit whose first parent is the journaled pre-pick HEAD (a
	// cherry-pick creates a copy, never the original commit; a continued
	// pick holds the engine's resolution, so the tree is not compared).
	ancestor, err := isAncestor(ctx, e.runner, p.Dir, p.Commit, "HEAD")
	if err != nil {
		return err
	}
	if ancestor {
		return nil
	}
	pickApplied, err := pickApplied(ctx, e.runner, p.Dir, p.Before)
	if err != nil {
		return err
	}
	if pickApplied {
		return nil
	}
	// Idempotency for the crash window between a conflicted Apply and its
	// failed row: this pick already ran and stopped on the conflict when
	// CHERRY_PICK_HEAD names it. A different pick in progress is a loud
	// error — never something to stack on.
	if picking, ok, err := cherryPickHead(ctx, e.runner, p.Dir); err != nil {
		return err
	} else if ok {
		if picking != p.Commit {
			return fmt.Errorf("gitx: cherry-pick %s in %s: a cherry-pick of %s is already in progress",
				p.Commit, p.Dir, picking)
		}
		return nil
	}
	if _, err := e.runner.Run(ctx, p.Dir, "cherry-pick", p.Commit); err != nil {
		if inCherryPick(ctx, e.runner, p.Dir) {
			files, ferr := conflictedFiles(ctx, e.runner, p.Dir)
			if ferr != nil {
				return fmt.Errorf("gitx: cherry-pick %s in %s: %w", p.Commit, p.Dir, err)
			}
			return &ConflictError{Files: files}
		}
		return fmt.Errorf("gitx: cherry-pick %s in %s: %w", p.Commit, p.Dir, err)
	}
	return nil
}

func (e *cherryPickEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeCherryPick(rec)
	if err != nil {
		return err
	}
	parent, err := revParse(ctx, e.runner, p.Dir, "HEAD^", "")
	if err != nil {
		return fmt.Errorf("gitx: undo cherry-pick %s in %s: %w", p.Commit, p.Dir, err)
	}
	if parent != p.Before {
		return fmt.Errorf("gitx: undo cherry-pick %s in %s: HEAD^ %s does not match pre-pick %s",
			p.Commit, p.Dir, parent, p.Before)
	}
	if _, err := e.runner.Run(ctx, p.Dir, "reset", "--hard", p.Before); err != nil {
		return fmt.Errorf("gitx: undo cherry-pick %s in %s: %w", p.Commit, p.Dir, err)
	}
	return nil
}

// pickApplied reports whether HEAD already holds the pick's outcome: HEAD
// moved off the journaled pre-pick HEAD by exactly its first parent —
// HEAD's first parent is Before. Between this effect's Prepare and a
// recovery re-apply, the only writers on the branch are the pick itself
// and its engine-resolved continuation, and both commit on top of Before
// (a conflicted pick does not move HEAD). A HEAD still equal to Before,
// or without a parent, is never an applied pick.
func pickApplied(ctx context.Context, r Runner, dir, before string) (bool, error) {
	parent, err := revParse(ctx, r, dir, "HEAD^", "")
	if err != nil {
		return false, nil // root commit: nothing was picked on top
	}
	return parent == before, nil
}

// cherryPickHead resolves the commit an in-progress cherry-pick is applying
// (CHERRY_PICK_HEAD); ok is false when no pick is in progress.
func cherryPickHead(ctx context.Context, r Runner, dir string) (commit string, ok bool, err error) {
	out, err := r.Run(ctx, dir, "rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD")
	if err == nil {
		return strings.TrimSpace(out), true, nil
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return "", false, nil
	}
	return "", false, fmt.Errorf("gitx: rev-parse CHERRY_PICK_HEAD in %s: %w", dir, err)
}

// cherryPickContinueOperation is the journaled finish of a conflict
// resolution.
type cherryPickContinueOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	payload any
	effects []operation.Effect
}

func (o *cherryPickContinueOperation) ID() identity.OperationID    { return o.id }
func (o *cherryPickContinueOperation) Kind() string                { return OpKindCherryPickContinue }
func (o *cherryPickContinueOperation) WorkID() identity.WorkID     { return o.workID }
func (o *cherryPickContinueOperation) Generation() int64           { return 0 }
func (o *cherryPickContinueOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *cherryPickContinueOperation) Payload() any                { return o.payload }
func (o *cherryPickContinueOperation) Effects() []operation.Effect { return o.effects }

// cherryPickContinuePayload is what the continue effect persists; Before is
// the conflicted HEAD the resolution commit is built on.
type cherryPickContinuePayload struct {
	Dir    string `json:"dir"`
	Before string `json:"before"`
}

// cherryPickContinueEffect finishes an in-progress cherry-pick after the
// engine resolved and staged the conflicts. Apply is idempotent: no
// cherry-pick in progress is success (already continued, or never started).
// The pinned GIT_EDITOR=true in the runner means git never opens an editor
// here. Revert discards the resolution commit, restoring the conflicted
// HEAD.
type cherryPickContinueEffect struct {
	runner  Runner
	payload cherryPickContinuePayload
}

func (e *cherryPickContinueEffect) Kind() string { return kindCherryPickContinue }

func (e *cherryPickContinueEffect) Prepare(ctx context.Context) (any, error) {
	before, err := revParse(ctx, e.runner, e.payload.Dir, "HEAD", "")
	if err != nil {
		return nil, err
	}
	p := e.payload
	p.Before = before
	return p, nil
}

func (e *cherryPickContinueEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeCherryPickContinue(rec)
	if err != nil {
		return err
	}
	if !inCherryPick(ctx, e.runner, p.Dir) {
		return nil // already continued
	}
	if _, err := e.runner.Run(ctx, p.Dir, "cherry-pick", "--continue"); err != nil {
		return fmt.Errorf("gitx: cherry-pick --continue in %s: %w", p.Dir, err)
	}
	return nil
}

func (e *cherryPickContinueEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeCherryPickContinue(rec)
	if err != nil {
		return err
	}
	head, err := revParse(ctx, e.runner, p.Dir, "HEAD", "")
	if err != nil {
		return err
	}
	if head == p.Before {
		return nil
	}
	if _, err := e.runner.Run(ctx, p.Dir, "reset", "--hard", p.Before); err != nil {
		return fmt.Errorf("gitx: undo cherry-pick --continue in %s: %w", p.Dir, err)
	}
	return nil
}

// cherryPickAbortOperation is the journaled abandonment of an integration:
// it aborts the in-progress cherry-pick and removes the integration
// worktree.
type cherryPickAbortOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	payload any
	effects []operation.Effect
}

func (o *cherryPickAbortOperation) ID() identity.OperationID    { return o.id }
func (o *cherryPickAbortOperation) Kind() string                { return OpKindCherryPickAbort }
func (o *cherryPickAbortOperation) WorkID() identity.WorkID     { return o.workID }
func (o *cherryPickAbortOperation) Generation() int64           { return 0 }
func (o *cherryPickAbortOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *cherryPickAbortOperation) Payload() any                { return o.payload }
func (o *cherryPickAbortOperation) Effects() []operation.Effect { return o.effects }

// cherryPickAbortPayload is what the abort effect persists.
type cherryPickAbortPayload struct {
	Dir string `json:"dir"`
}

// cherryPickAbortEffect discards an in-progress cherry-pick. Apply is
// idempotent: no cherry-pick in progress is success. Revert is a no-op and
// documented: the discarded conflict state cannot be restored, so a roll-
// back of an abort is a leak by design (ADR 0025) — the abort operation's
// own recovery always rolls forward to finish the cleanup.
type cherryPickAbortEffect struct {
	runner  Runner
	payload cherryPickAbortPayload
}

func (e *cherryPickAbortEffect) Kind() string { return kindCherryPickAbort }

func (e *cherryPickAbortEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *cherryPickAbortEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeCherryPickAbort(rec)
	if err != nil {
		return err
	}
	if !inCherryPick(ctx, e.runner, p.Dir) {
		return nil // already aborted
	}
	if _, err := e.runner.Run(ctx, p.Dir, "cherry-pick", "--abort"); err != nil {
		return fmt.Errorf("gitx: cherry-pick --abort in %s: %w", p.Dir, err)
	}
	return nil
}

func (e *cherryPickAbortEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	return nil // cannot restore a discarded conflict state (ADR 0025 leak)
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

func decodeCherryPick(rec operation.EffectRecord) (cherryPickPayload, error) {
	var p cherryPickPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, fmt.Errorf("gitx: decode cherry-pick payload: %w", err)
	}
	return p, nil
}

func decodeCherryPickContinue(rec operation.EffectRecord) (cherryPickContinuePayload, error) {
	var p cherryPickContinuePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, fmt.Errorf("gitx: decode cherry-pick-continue payload: %w", err)
	}
	return p, nil
}

func decodeCherryPickAbort(rec operation.EffectRecord) (cherryPickAbortPayload, error) {
	var p cherryPickAbortPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return p, fmt.Errorf("gitx: decode cherry-pick-abort payload: %w", err)
	}
	return p, nil
}

// worktreeRegisteredWith is the free-function form of Service.worktreeRegistered
// so effects (which carry only a Runner) can check registration during
// recovery dispatch.
func worktreeRegisteredWith(ctx context.Context, r Runner, repoDir, path string) (WorktreeEntry, bool, error) {
	out, err := r.Run(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return WorktreeEntry{}, false, fmt.Errorf("gitx: worktree list of %s: %w", repoDir, err)
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
	for _, e := range entries {
		if samePath(e.Path, path) {
			return e, true, nil
		}
	}
	return WorktreeEntry{}, false, nil
}
