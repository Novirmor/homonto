package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/store"
)

// createLeasePayload is the journalled identity of one lease creation: the
// path and the full content, recovery token included. Recovery replays this
// payload, so re-apply writes the same token the original run minted.
type createLeasePayload struct {
	Path    string       `json:"path"`
	Content LeaseContent `json:"content"`
}

// createLeaseEffect creates one lease file with O_EXCL semantics. Apply is
// idempotent: a file already holding exactly this content is a no-op (the
// unrecorded-window re-apply); a file holding anything else is a conflict
// naming the owner. Revert removes only the token-matching lease and leaves
// a foreign one alone.
type createLeaseEffect struct {
	payload createLeasePayload
}

// NewCreateLeaseEffect returns the journaled effect that creates the lease
// at path with content. The workspace rescan path uses it for added members.
func NewCreateLeaseEffect(path string, content LeaseContent) operation.Effect {
	return &createLeaseEffect{payload: createLeasePayload{Path: path, Content: content}}
}

func (e *createLeaseEffect) Kind() string { return kindCreateLease }

func (e *createLeaseEffect) Prepare(ctx context.Context) (any, error) {
	return e.payload, nil
}

func (e *createLeaseEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p createLeasePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode create payload of effect %d: %w", rec.Seq, err)
	}
	return createLeaseFile(p.Path, p.Content)
}

func (e *createLeaseEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p createLeasePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode create payload of effect %d: %w", rec.Seq, err)
	}
	return removeMatchingLease(p.Path, p.Content)
}

// createLeaseFile writes the lease at path, or no-ops when the existing file
// already carries exactly this content.
func createLeaseFile(path string, content LeaseContent) error {
	data, err := content.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("lease: mkdir %s: %w", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.CreateExclusive(filepath.Base(path), data, leaseMode)
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	existing, rerr := readLeaseFile(path)
	if rerr != nil {
		return fmt.Errorf("lease: %s already present and unreadable: %v: %w", path, rerr, ErrLeaseConflict)
	}
	if existing != content {
		return fmt.Errorf("lease: %s is held by workspace %s (work %s, generation %d): %w",
			path, existing.WorkspaceID, existing.WorkID, existing.Generation, ErrLeaseConflict)
	}
	return nil
}

// removeMatchingLease removes the lease at path only when its content equals
// the journaled content; a missing file is a no-op and a foreign file is
// left alone.
func removeMatchingLease(path string, content LeaseContent) error {
	existing, err := readLeaseFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if existing != content {
		return fmt.Errorf("lease: %s is held by workspace %s, not the lease being released: %w",
			path, existing.WorkspaceID, ErrLeaseConflict)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.Base(path)); err != nil {
		return fmt.Errorf("lease: remove %s: %w", path, err)
	}
	return nil
}

// sentinelPayload is the journalled identity of the checkpoint commit
// marker write.
type sentinelPayload struct {
	Path    string          `json:"path"`
	Content SentinelContent `json:"content"`
}

// sentinelEffect writes the checkpoint commit marker atomically. Apply is
// idempotent (deterministic content per operation). Revert must never run:
// once the marker exists, activation is never rolled back, so a roll-back
// reaching this effect is an invariant violation and fails loudly.
type sentinelEffect struct {
	payload sentinelPayload
}

func (e *sentinelEffect) Kind() string { return kindSentinel }

func (e *sentinelEffect) Prepare(ctx context.Context) (any, error) {
	return e.payload, nil
}

func (e *sentinelEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode sentinel payload of effect %d: %w", rec.Seq, err)
	}
	data, err := p.Content.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return fmt.Errorf("lease: mkdir %s: %w", filepath.Dir(p.Path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(p.Path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(p.Path), data, leaseMode); err != nil {
		return fmt.Errorf("lease: write commit marker %s: %w", p.Path, err)
	}
	return nil
}

func (e *sentinelEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	return fmt.Errorf("lease: commit marker effect of %s must never be reverted (activation is never rolled back)", rec.OpID)
}

// activatePayload is the journalled identity of the activation transaction.
type activatePayload struct {
	WorkID      identity.WorkID      `json:"work_id"`
	OperationID identity.OperationID `json:"operation_id"`
	WorkKind    string               `json:"work_kind"`
	Title       string               `json:"title"`
}

// activateEffect commits — in one SQLite transaction — the work's active row
// and the operation-applied marker. It runs only after the commit marker is
// durable, so a roll-back can never reach it; Apply is an idempotent upsert
// and Revert is the same invariant error as the sentinel's. The registered
// recovery prototype carries the database, so the effect stays stateless
// with respect to its journalled identity.
type activateEffect struct {
	payload activatePayload
	db      *store.DB
}

func (e *activateEffect) Kind() string { return kindActivate }

func (e *activateEffect) Prepare(ctx context.Context) (any, error) {
	return e.payload, nil
}

func (e *activateEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p activatePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode activate payload of effect %d: %w", rec.Seq, err)
	}
	now := time.Now().UTC()
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if err := tx.UpsertWork(ctx, store.WorkRecord{
			ID: p.WorkID, Kind: p.WorkKind, Title: p.Title,
			State: workActiveState, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.SetMeta(ctx, metaAppliedPrefix+string(p.OperationID), "1")
	})
}

func (e *activateEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	return fmt.Errorf("lease: activation effect of %s must never be reverted (activation is never rolled back)", rec.OpID)
}

// removeLeasePayload is the journalled identity of one lease release.
type removeLeasePayload struct {
	Path    string       `json:"path"`
	Content LeaseContent `json:"content"`
}

// removeLeaseEffect releases one lease: Apply removes only the
// token-matching file (missing is a no-op), Revert re-creates it with O_EXCL
// so a roll-back of a rescan restore the member's lease without ever
// overwriting a foreign one.
type removeLeaseEffect struct {
	payload removeLeasePayload
}

// NewRemoveLeaseEffect returns the journaled effect that releases the lease
// at path. The workspace rescan path uses it for removed members.
func NewRemoveLeaseEffect(path string, content LeaseContent) operation.Effect {
	return &removeLeaseEffect{payload: removeLeasePayload{Path: path, Content: content}}
}

func (e *removeLeaseEffect) Kind() string { return kindRemoveLease }

func (e *removeLeaseEffect) Prepare(ctx context.Context) (any, error) {
	return e.payload, nil
}

func (e *removeLeaseEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p removeLeasePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode remove payload of effect %d: %w", rec.Seq, err)
	}
	return removeMatchingLease(p.Path, p.Content)
}

func (e *removeLeaseEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p removeLeasePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("lease: decode remove payload of effect %d: %w", rec.Seq, err)
	}
	return createLeaseFile(p.Path, p.Content)
}

// readLeaseFile reads and strictly decodes the lease file at path,
// translating a missing file into fs.ErrNotExist.
func readLeaseFile(path string) (LeaseContent, error) {
	data, err := readControlFile(path)
	if err != nil {
		return LeaseContent{}, err
	}
	content, err := ReadBytes(data)
	if err != nil {
		return LeaseContent{}, fmt.Errorf("%s: %w", path, err)
	}
	return content, nil
}

// readControlFile reads one control-plane file through securefs.
func readControlFile(path string) ([]byte, error) {
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(filepath.Base(path))
}
