// The integration reset effect that returns an integration area to its base.
package gitx

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/operation"
)

// integrationResetPayload is what the reset effect persists.
type integrationResetPayload struct {
	RepoDir string `json:"repo_dir"`
	Path    string `json:"path"`
	Base    string `json:"base"`
}

// integrationResetEffect returns an existing integration worktree to the
// base its round starts from.
//
// An integration area is named for its work and member, so a second
// integration round — the one that follows a repair — finds the first
// round's area still holding the first round's materials. Cherry-picking
// this round's materials on top of that integrates work that was already
// superseded: git either stops with "the previous cherry-pick is now
// empty" or, worse, succeeds and leaves the failed attempt on the branch
// while the record says the checks passed.
//
// So a round starts from the base, always. A DIRTY area is refused rather
// than reset: uncommitted changes there are someone's unfinished conflict
// resolution, and discarding that silently is not a decision this code
// gets to make.
type integrationResetEffect struct {
	runner  Runner
	payload integrationResetPayload
}

func (e *integrationResetEffect) Kind() string { return kindIntegrationReset }

func (e *integrationResetEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *integrationResetEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodeIntegrationReset(rec)
	if err != nil {
		return err
	}
	_, registered, err := worktreeRegisteredWith(ctx, e.runner, p.RepoDir, p.Path)
	if err != nil {
		return err
	}
	if !registered {
		return nil // the create effect made it, and a fresh worktree is at base
	}
	head, err := revParse(ctx, e.runner, p.Path, "HEAD", "")
	if err != nil {
		return err
	}
	if head == p.Base {
		return nil // already applied
	}
	files, err := dirtyPaths(ctx, e.runner, p.Path)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return &DirtyWorktreeError{Files: files}
	}
	if _, err := e.runner.Run(ctx, p.Path, "reset", "--hard", p.Base); err != nil {
		return fmt.Errorf("gitx: reset integration %s to %s: %w", p.Path, p.Base, err)
	}
	return nil
}

// Revert does nothing: the previous round's commits are reachable from the
// materials they were cherry-picked from, so there is nothing this effect
// destroyed that rolling back could or should restore.
func (e *integrationResetEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	return nil
}

// decodeIntegrationReset reads the reset payload back from the journal.
func decodeIntegrationReset(rec operation.EffectRecord) (integrationResetPayload, error) {
	var p integrationResetPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return integrationResetPayload{}, fmt.Errorf("gitx: decode integration reset payload: %w", err)
	}
	return p, nil
}
