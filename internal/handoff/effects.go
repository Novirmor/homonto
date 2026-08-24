package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/store"
)

// Effect kinds registered for recovery dispatch.
const (
	kindCheckpointWrite = "handoff.checkpoint_write"
	kindSentinelWrite   = "handoff.sentinel_write"
	kindSentinelRemove  = "handoff.sentinel_remove"
	kindCommit          = "handoff.commit"
	kindClaim           = "handoff.claim"
	kindRebuild         = "handoff.rebuild"
)

// registerEffects installs every handoff effect prototype for recovery
// dispatch. The rebuild prototype carries the database (the dispatch
// context, not journalled identity). Registration is idempotent.
func registerEffects(ops *operation.Manager, db *store.DB) {
	ops.RegisterEffect(&checkpointWriteEffect{})
	ops.RegisterEffect(&sentinelWriteEffect{})
	ops.RegisterEffect(&sentinelRemoveEffect{})
	ops.RegisterEffect(&commitEffect{})
	ops.RegisterEffect(&claimEffect{})
	ops.RegisterEffect(&rebuildEffect{db: db})
}

// sortByRepoID sorts lease-list entries by repository id.
func sortByRepoID(ls []lease.SentinelLease) {
	sort.Slice(ls, func(i, j int) bool { return ls[i].RepositoryID < ls[j].RepositoryID })
}

// checkpointWritePayload is the journalled identity of one checkpoint slot
// rewrite: the full next checkpoint plus the previous one for revert.
type checkpointWritePayload struct {
	Path string                `json:"path"`
	Next checkpoint.Checkpoint `json:"next"`
	Prev checkpoint.Checkpoint `json:"prev"`
}

// checkpointWriteEffect atomically rewrites the checkpoint slot. Apply and
// Revert are idempotent: both write deterministic canonical bytes, so the
// unrecorded-window re-apply and re-revert converge.
type checkpointWriteEffect struct {
	payload checkpointWritePayload
}

func (e *checkpointWriteEffect) Kind() string { return kindCheckpointWrite }

func (e *checkpointWriteEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *checkpointWriteEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p checkpointWritePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode checkpoint payload of effect %d: %w", rec.Seq, err)
	}
	return writeCheckpointFile(p.Path, p.Next)
}

func (e *checkpointWriteEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p checkpointWritePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode checkpoint payload of effect %d: %w", rec.Seq, err)
	}
	return writeCheckpointFile(p.Path, p.Prev)
}

// writeCheckpointFile canonically encodes cp and atomically replaces the
// checkpoint at path (owner-only, per the checkpoint secrecy contract).
func writeCheckpointFile(path string, cp checkpoint.Checkpoint) error {
	data, err := checkpoint.Encode(cp)
	if err != nil {
		return err
	}
	return writeAtomicFile(path, data, 0o600)
}

// sentinelPayload is the journalled identity of one sentinel file write or
// removal.
type sentinelPayload struct {
	Path    string                `json:"path"`
	Content lease.SentinelContent `json:"content"`
}

// sentinelWriteEffect writes the lease sentinel at attach. Revert removes
// it again (content-matched, missing tolerated), so a failed attach leaves
// no marker behind.
type sentinelWriteEffect struct {
	payload sentinelPayload
}

func (e *sentinelWriteEffect) Kind() string { return kindSentinelWrite }

func (e *sentinelWriteEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *sentinelWriteEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode sentinel payload of effect %d: %w", rec.Seq, err)
	}
	data, err := p.Content.Marshal()
	if err != nil {
		return err
	}
	return writeAtomicFile(p.Path, data, 0o600)
}

func (e *sentinelWriteEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode sentinel payload of effect %d: %w", rec.Seq, err)
	}
	return removeMatchingSentinel(p.Path, p.Content)
}

// sentinelRemoveEffect removes the lease sentinel at handoff: the leases it
// described are being released and attach re-creates it. Revert restores
// the journalled content.
type sentinelRemoveEffect struct {
	payload sentinelPayload
}

func (e *sentinelRemoveEffect) Kind() string { return kindSentinelRemove }

func (e *sentinelRemoveEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *sentinelRemoveEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode sentinel payload of effect %d: %w", rec.Seq, err)
	}
	if _, err := os.Stat(p.Path); errors.Is(err, fs.ErrNotExist) {
		return nil // idempotent re-apply
	} else if err != nil {
		return fmt.Errorf("handoff: stat sentinel %s: %w", p.Path, err)
	}
	if err := os.Remove(p.Path); err != nil {
		return fmt.Errorf("handoff: remove sentinel %s: %w", p.Path, err)
	}
	return nil
}

func (e *sentinelRemoveEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode sentinel payload of effect %d: %w", rec.Seq, err)
	}
	data, err := p.Content.Marshal()
	if err != nil {
		return err
	}
	return writeAtomicFile(p.Path, data, 0o600)
}

// removeMatchingSentinel removes the sentinel at path only when its content
// equals the journalled content; a missing file is a no-op.
func removeMatchingSentinel(path string, content lease.SentinelContent) error {
	existing, err := lease.ReadSentinel(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	want, err := content.Marshal()
	if err != nil {
		return err
	}
	have, err := existing.Marshal()
	if err != nil {
		return err
	}
	if string(have) != string(want) {
		return fmt.Errorf("handoff: sentinel %s differs from the journalled marker", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("handoff: remove sentinel %s: %w", path, err)
	}
	return nil
}

// commitPayload is the journalled identity of one control-repository
// commit.
type commitPayload struct {
	Root    string `json:"root"`
	Message string `json:"message"`
	// Required marks the portable handoff commit: staging nothing is
	// ErrNothingToCommit (an identical HEAD message is the idempotent
	// re-apply). Attach commits are best-effort: nothing staged is skipped.
	Required bool `json:"required"`
}

// commitEffect stages the portable artifacts (.homonto/checkpoint.json,
// .homonto/config.toml, docs/homonto when present) and commits them under
// the homonto bot identity. The author is passed per-invocation through -c
// user.name/user.email argument vectors — git's global configuration is
// never read or mutated, and commit signing is disabled the same way.
//
// Apply is idempotent: after the commit exists, staging finds nothing and
// the HEAD message comparison recognizes the completed commit. Revert
// cannot uncommit and is a documented no-op leak (ADR 0025) — a rolled-back
// attach that already committed leaves the commit; the checkpoint revert
// restores the working tree, which is what the next attempt diffs against.
type commitEffect struct {
	payload commitPayload
}

// Bot identity for control commits; per-invocation -c arguments only.
const (
	commitUserName  = "homonto"
	commitUserEmail = "homonto@localhost"
)

func (e *commitEffect) Kind() string { return kindCommit }

func (e *commitEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *commitEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p commitPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode commit payload of effect %d: %w", rec.Seq, err)
	}
	runner := gitx.ExecRunner{}
	if !isGitRepository(ctx, runner, p.Root) {
		if p.Required {
			return fmt.Errorf("handoff: commit %s: control root is not a git repository: %w", p.Root, ErrNothingToCommit)
		}
		return nil
	}
	args := []string{"add", "-A", "-f", "--"}
	var pathspecs []string
	for _, rel := range []string{".homonto/checkpoint.json", ".homonto/config.toml", "docs/homonto"} {
		if _, err := os.Stat(filepath.Join(p.Root, filepath.FromSlash(rel))); err == nil {
			pathspecs = append(pathspecs, rel)
		}
	}
	if len(pathspecs) == 0 {
		if p.Required {
			if head, herr := runner.Run(ctx, p.Root, "log", "-1", "--pretty=%s"); herr == nil &&
				trimSpace(head) == p.Message {
				return nil // idempotent re-apply of an already-made commit
			}
			return fmt.Errorf("handoff: commit %q: %w", p.Message, ErrNothingToCommit)
		}
		return nil
	}
	if _, err := runner.Run(ctx, p.Root, append(args, pathspecs...)...); err != nil {
		return fmt.Errorf("handoff: stage portable artifacts: %w", err)
	}
	staged, err := stagedChanges(ctx, runner, p.Root)
	if err != nil {
		return err
	}
	if !staged {
		if p.Required {
			if head, herr := runner.Run(ctx, p.Root, "log", "-1", "--pretty=%s"); herr == nil &&
				trimSpace(head) == p.Message {
				return nil // idempotent re-apply of an already-made commit
			}
			return fmt.Errorf("handoff: commit %q: %w", p.Message, ErrNothingToCommit)
		}
		return nil
	}
	if _, err := runner.Run(ctx, p.Root,
		"-c", "user.name="+commitUserName,
		"-c", "user.email="+commitUserEmail,
		"-c", "commit.gpgsign=false",
		"commit", "-m", p.Message); err != nil {
		return fmt.Errorf("handoff: commit %q: %w", p.Message, err)
	}
	return nil
}

func (e *commitEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	return nil // cannot uncommit; documented leak
}

// isGitRepository reports whether root is a usable git repository.
func isGitRepository(ctx context.Context, runner gitx.Runner, root string) bool {
	_, err := runner.Run(ctx, root, "rev-parse", "--git-common-dir")
	return err == nil
}

// stagedChanges reports whether the index differs from HEAD.
func stagedChanges(ctx context.Context, runner gitx.Runner, root string) (bool, error) {
	_, err := runner.Run(ctx, root, "diff", "--cached", "--quiet")
	if err == nil {
		return false, nil
	}
	var ce *gitx.CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return true, nil
	}
	return false, fmt.Errorf("handoff: inspect staged changes: %w", err)
}

// claimPayload is the journalled identity of one registration claim.
type claimPayload struct {
	Path         string                    `json:"path"`
	Registration registration.Registration `json:"registration"`
}

// claimEffect claims a member's registration slot with O_EXCL semantics.
// Apply no-ops when the slot already holds exactly this registration (the
// unrecorded-window re-apply); Revert removes only the workspace's own
// claim.
type claimEffect struct {
	payload claimPayload
}

func (e *claimEffect) Kind() string { return kindClaim }

func (e *claimEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *claimEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p claimPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode claim payload of effect %d: %w", rec.Seq, err)
	}
	if err := registration.Claim(p.Path, p.Registration); err != nil {
		if errors.Is(err, registration.ErrOwnedByOther) {
			existing, rerr := registration.Read(p.Path)
			if rerr == nil && existing == p.Registration {
				return nil
			}
		}
		return err
	}
	return nil
}

func (e *claimEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p claimPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode claim payload of effect %d: %w", rec.Seq, err)
	}
	if err := registration.Detach(p.Path, p.Registration.WorkspaceID); err != nil {
		if errors.Is(err, registration.ErrNotRegistered) {
			return nil
		}
		return err
	}
	return nil
}

// trimSpace is strings.TrimSpace (local alias to keep imports tight).
func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
