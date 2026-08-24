package gitx

import (
	"context"
	"fmt"
)

// Recover registers every git effect kind and drives every pending git
// operation to a terminal state per its journaled policy. All git
// operations journal RollForward: an interrupted worktree creation,
// cherry-pick, continue, abort, or cleanup finishes on recovery — the
// idempotent Apply re-runs onto the side effect it already performed.
// Call Recover once after opening the runtime database before serving work.
func (s *Service) Recover(ctx context.Context) error {
	s.registerEffects()
	if err := s.ops.RecoverPending(ctx); err != nil {
		return fmt.Errorf("gitx: recover: %w", err)
	}
	return nil
}
