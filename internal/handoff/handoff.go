// Package handoff implements the portable workflow handoff: preparing a
// workspace's active work for takeover by another machine, proposing and
// confirming member mappings against a fresh clone, and attaching the
// transferable checkpoint as the new machine's consumed, leased, and rebuilt
// runtime state.
//
// # Portable handoff (`handoff --portable`, PreparePortable)
//
// PreparePortable is one journaled, roll-forward operation that (in apply
// order) writes the checkpoint's local→transferable transition at generation
// +1 with a fresh transfer id (or the request's explicit one), releases
// every member lease the checkpoint
// commit marker lists (token-matching removals), removes the marker —
// leases are gone; attach re-creates them — and commits the portable
// artifacts (checkpoint, config, docs/homonto) to the control repository
// under the homonto bot identity (`-c user.name=…` argument vectors, never a
// global git-config mutation). A commit with nothing staged is
// ErrNothingToCommit: the checkpoint transition must be committed for the
// handoff to be portable at all. An in-process failure rolls the whole
// operation back — checkpoint restored, leases and marker re-created.
//
// # Attach (`homonto attach`, Attach)
//
// Attach loads the checkpoint from a clone of the control repository and
// requires the transferable state at the recorded generation. A consumed
// checkpoint is the typed ErrCheckpointConsumed unless Force is set: force is
// the human-confirmed takeover, which first re-marks the checkpoint
// transferable at generation +1 (the only legal way out of consumed —
// ValidateTransition demands the bump), records a forced_takeover decision
// in the runtime database, and marks all evidence stale via the
// evidence_stale meta key before consuming at the bumped generation.
//
// The attach itself is one journaled all-or-none operation: claim every
// checkpoint member's registration (sorted by repository id), acquire the
// full lease set at the checkpoint generation (fresh tokens minted at
// prepare time and journaled, so crash recovery replays the same tokens),
// re-create the lease sentinel, rebuild the runtime database from portable
// inputs, mark the checkpoint consumed at the same generation and transfer
// id, and — when the control repository is dirty after that — commit
// `homonto: attach <generation>`. Every effect has a real revert, so a
// failed attach (a foreign registration, a broken commit hook) leaves no
// claims, leases, sentinel, or runtime rows behind. Actions, reports, and
// decisions from the old machine are never recreated: attach issues fresh
// action identities under a newly minted runtime HMAC key, so every
// freshness token from before the handoff fails verification.
//
// # Stale-clone refusal, honestly scoped
//
// Homonto enforces process ownership, not a distributed lock: it cannot
// prevent an offline stale clone from being modified. What it does enforce
// is refusal at the next observation — the stale runtime's lease set is gone
// (PreparePortable released it), so lease.ValidateAll fails with
// ErrLeaseMissing/ErrLeaseDrift, and CheckpointGeneration reports a
// generation the stale runtime does not hold. Engines consult both before
// mutating.
//
// # Crash model
//
// Every step is an idempotent effect under the operation journal (ADR 0025):
// crash recovery rolls a prepared handoff or attach forward to convergence;
// an operation that only reached pending never ran a side effect and
// recovers as a clean rollback, after which the command can simply be run
// again. Recover is the entry point that drives both this package's and the
// lease package's pending operations to terminal states.
package handoff

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Typed errors. Wrap with context via fmt.Errorf("%w", ...) so callers can
// branch with errors.Is.
var (
	// ErrNotLocal: PreparePortable found a checkpoint that is not in the
	// local handoff state (already transferable or consumed).
	ErrNotLocal = errors.New("handoff: checkpoint is not local")
	// ErrNotTransferable: Attach found a checkpoint that is still local.
	ErrNotTransferable = errors.New("handoff: checkpoint is not transferable")
	// ErrCheckpointConsumed: the checkpoint was already attached at this
	// generation; attach again only with an explicit forced takeover.
	ErrCheckpointConsumed = errors.New("handoff: checkpoint already consumed at this generation")
	// ErrNoActiveWork: the checkpoint or lease sentinel does not describe
	// the requested work.
	ErrNoActiveWork = errors.New("handoff: no active work for this handoff")
	// ErrGenerationMismatch: the lease sentinel's generation disagrees with
	// the checkpoint's handoff generation.
	ErrGenerationMismatch = errors.New("handoff: sentinel generation does not match checkpoint")
	// ErrNothingToCommit: the required control commit found nothing staged.
	ErrNothingToCommit = errors.New("handoff: nothing to commit")
	// ErrMappingIncomplete: the confirmed mappings do not cover exactly the
	// checkpoint's members.
	ErrMappingIncomplete = errors.New("handoff: member mapping incomplete or invalid")
	// ErrMemberUnusable: a confirmed mapping's path is not a member root of
	// the declared kind.
	ErrMemberUnusable = errors.New("handoff: mapped member not usable on disk")
)

// CheckpointPath returns the portable checkpoint's path under a control
// root: <control-root>/.homonto/checkpoint.json.
func CheckpointPath(controlRoot string) string {
	return filepath.Join(controlRoot, ".homonto", "checkpoint.json")
}

// RuntimeDBPath returns the local runtime database path under a control
// root. The database is never committed; it is rebuilt by attach.
func RuntimeDBPath(controlRoot string) string {
	return filepath.Join(controlRoot, ".homonto", "runtime.db")
}

// CheckpointGeneration reads and strictly decodes the checkpoint under
// controlRoot and returns its handoff generation — the generation that may
// currently attach. Engines consult this before mutating shared state so a
// stale clone whose leases and generation no longer match refuses work; it
// is a process-ownership check, not a distributed lock (see the package
// doc's stale-clone section).
func CheckpointGeneration(controlRoot string) (uint64, error) {
	cp, _, err := checkpoint.Load(CheckpointPath(controlRoot))
	if err != nil {
		return 0, fmt.Errorf("handoff: checkpoint generation of %s: %w", controlRoot, err)
	}
	return cp.Handoff.Generation, nil
}

// PortableRequest names the active work being prepared for portable
// handoff on the machine that currently holds it.
type PortableRequest struct {
	WorkspaceID identity.WorkspaceID
	WorkID      identity.WorkID
	// ControlRoot is the canonical absolute path of the control repository.
	ControlRoot string
	// Git runs the control commit; nil means gitx.ExecRunner{}.
	Git gitx.Runner
	// TransferID, when set, is the transition's transfer id instead of a
	// freshly minted one — the deterministic re-preparation knob recovery
	// tooling and tests use to replay a known transition (a re-preparation
	// that would otherwise mint a fresh id and commit a second time).
	TransferID identity.Token
}

// portablePayload is the journaled parameters of the portable handoff.
type portablePayload struct {
	WorkspaceID identity.WorkspaceID `json:"workspace_id"`
	WorkID      identity.WorkID      `json:"work_id"`
	FromGen     uint64               `json:"from_generation"`
	ToGen       uint64               `json:"to_generation"`
	ControlRoot string               `json:"control_root"`
	TransferID  identity.Token       `json:"transfer_id"`
}

// portableOperation is the journaled portable handoff.
type portableOperation struct {
	id         identity.OperationID
	workID     identity.WorkID
	generation uint64
	payload    portablePayload
	effects    []operation.Effect
}

func (o *portableOperation) ID() identity.OperationID    { return o.id }
func (o *portableOperation) Kind() string                { return "handoff.portable" }
func (o *portableOperation) WorkID() identity.WorkID     { return o.workID }
func (o *portableOperation) Generation() int64           { return int64(o.generation) }
func (o *portableOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *portableOperation) Payload() any                { return o.payload }
func (o *portableOperation) Effects() []operation.Effect { return o.effects }

// PreparePortable marks the active work's checkpoint transferable and
// commits it, releasing the local leases that anchored it. See the package
// doc for the step order, crash behavior, and the nothing-to-commit rule.
func PreparePortable(ctx context.Context, req PortableRequest) error {
	if err := identity.ValidateUUID(string(req.WorkspaceID)); err != nil {
		return fmt.Errorf("handoff: workspace_id: %w", err)
	}
	if err := identity.ValidateUUID(string(req.WorkID)); err != nil {
		return fmt.Errorf("handoff: work_id: %w", err)
	}
	root, err := workspace.CanonicalPath(req.ControlRoot)
	if err != nil {
		return err
	}
	runner := req.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}

	cp, cfg, err := loadCheckpointState(ctx, root)
	if err != nil {
		return err
	}
	if err := checkHandoffWork(cp, req.WorkspaceID, req.WorkID); err != nil {
		return err
	}

	db, err := openRuntime(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// A crashed earlier handoff (or any other pending operation) converges
	// before this one journals anything.
	if err := Recover(ctx, db); err != nil {
		return fmt.Errorf("handoff: prepare: recover pending operations: %w", err)
	}
	cp, cfg, err = loadCheckpointState(ctx, root)
	if err != nil {
		return err
	}
	if err := checkHandoffWork(cp, req.WorkspaceID, req.WorkID); err != nil {
		return err
	}

	sentinel, err := lease.ReadSentinel(lease.SentinelPath(root, req.WorkID))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("handoff: prepare %s: %w", req.WorkID, ErrNoActiveWork)
		}
		return fmt.Errorf("handoff: prepare: read lease sentinel: %w", err)
	}
	if sentinel.WorkspaceID != req.WorkspaceID || sentinel.WorkID != req.WorkID {
		return fmt.Errorf("handoff: prepare: sentinel names workspace %s work %s: %w",
			sentinel.WorkspaceID, sentinel.WorkID, ErrNoActiveWork)
	}
	if sentinel.Generation != cp.Handoff.Generation {
		return fmt.Errorf("handoff: prepare: sentinel generation %d, checkpoint generation %d: %w",
			sentinel.Generation, cp.Handoff.Generation, ErrGenerationMismatch)
	}

	next := cp
	transferID := req.TransferID
	if transferID == "" {
		transferID = mustNewToken()
	}
	if err := identity.ValidateToken(string(transferID)); err != nil {
		return fmt.Errorf("handoff: prepare: transfer id: %w", err)
	}
	next.Handoff = checkpoint.Handoff{
		State:      checkpoint.HandoffTransferable,
		Generation: cp.Handoff.Generation + 1,
		TransferID: transferID,
	}
	if err := checkpoint.ValidateTransition(cp, next); err != nil {
		return fmt.Errorf("handoff: prepare: %w", err)
	}
	if err := checkpoint.Validate(next, cfg); err != nil {
		return fmt.Errorf("handoff: prepare: transferable checkpoint: %w", err)
	}

	// Release exactly the leases the sentinel lists, reading each token-
	// matching content from disk before anything is journaled.
	ordered := append([]lease.SentinelLease(nil), sentinel.Leases...)
	sortByRepoID(ordered)
	var effects []operation.Effect
	effects = append(effects, &checkpointWriteEffect{payload: checkpointWritePayload{
		Path: CheckpointPath(root), Next: next, Prev: cp,
	}})
	for _, l := range ordered {
		content, err := lease.ReadLease(l.Path)
		if err != nil {
			return fmt.Errorf("handoff: prepare: read lease of %s: %w", l.RepositoryID, err)
		}
		effects = append(effects, lease.NewRemoveLeaseEffect(l.Path, content))
	}
	effects = append(effects, &sentinelRemoveEffect{payload: sentinelPayload{
		Path: lease.SentinelPath(root, req.WorkID), Content: sentinel,
	}})
	effects = append(effects, &commitEffect{payload: commitPayload{
		Root:     root,
		Message:  fmt.Sprintf("homonto: portable handoff %s", req.WorkID),
		Required: true,
	}})

	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("handoff: prepare: operation id: %w", err)
	}
	op := &portableOperation{
		id: opID, workID: req.WorkID, generation: next.Handoff.Generation,
		payload: portablePayload{
			WorkspaceID: req.WorkspaceID, WorkID: req.WorkID,
			FromGen: cp.Handoff.Generation, ToGen: next.Handoff.Generation,
			ControlRoot: root, TransferID: next.Handoff.TransferID,
		},
		effects: effects,
	}
	if err := runOperation(ctx, db, op); err != nil {
		return fmt.Errorf("handoff: prepare %s: %w", opID, err)
	}
	return nil
}

// checkHandoffWork verifies the checkpoint describes the requested
// workspace's active work and is still local (the prepare precondition).
func checkHandoffWork(cp checkpoint.Checkpoint, wsID identity.WorkspaceID, workID identity.WorkID) error {
	if cp.WorkspaceID != wsID {
		return fmt.Errorf("handoff: checkpoint names workspace %s, not %s: %w",
			cp.WorkspaceID, wsID, ErrNoActiveWork)
	}
	if cp.Work == nil || cp.Work.ID != workID {
		return fmt.Errorf("handoff: checkpoint does not describe work %s: %w", workID, ErrNoActiveWork)
	}
	if cp.Handoff.State != checkpoint.HandoffLocal {
		return fmt.Errorf("handoff: checkpoint is %s at generation %d: %w",
			cp.Handoff.State, cp.Handoff.Generation, ErrNotLocal)
	}
	return nil
}

// loadCheckpointState loads the checkpoint and the committed configuration
// it must validate against, cross-checking both before any mutation.
func loadCheckpointState(ctx context.Context, root string) (checkpoint.Checkpoint, workspacecfg.Config, error) {
	cp, _, err := checkpoint.Load(CheckpointPath(root))
	if err != nil {
		return checkpoint.Checkpoint{}, workspacecfg.Config{}, fmt.Errorf("handoff: load checkpoint: %w", err)
	}
	cfg, err := workspacecfg.Load(workspace.ConfigPath(root))
	if err != nil {
		return checkpoint.Checkpoint{}, workspacecfg.Config{}, fmt.Errorf("handoff: load config: %w", err)
	}
	if err := checkpoint.Validate(cp, cfg); err != nil {
		return checkpoint.Checkpoint{}, workspacecfg.Config{}, fmt.Errorf("handoff: checkpoint vs config: %w", err)
	}
	return cp, cfg, nil
}

// openRuntime opens (creating and migrating when absent) the runtime
// database under root.
func openRuntime(ctx context.Context, root string) (*store.DB, error) {
	if err := os.MkdirAll(filepath.Join(root, ".homonto"), 0o755); err != nil {
		return nil, fmt.Errorf("handoff: mkdir .homonto: %w", err)
	}
	db, err := store.Open(ctx, RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		return nil, fmt.Errorf("handoff: open runtime: %w", err)
	}
	return db, nil
}

// runOperation journals op and, on failure, drives it to a terminal state
// exactly as crash recovery would — the same cleanup contract the lease and
// rescan paths use.
func runOperation(ctx context.Context, db *store.DB, op operation.Operation) error {
	ops := operation.NewManager(db)
	_ = lease.NewManager(db, ops) // registers the lease effect kinds
	registerEffects(ops, db)
	if err := ops.Run(ctx, op); err != nil {
		cleanupErr := db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationPolicy(ctx, op.ID(), string(operation.RollBack))
		})
		if cleanupErr == nil {
			cleanupErr = ops.RecoverOne(ctx, op.ID())
		}
		if cleanupErr != nil {
			return fmt.Errorf("%v (cleanup: %v)", err, cleanupErr)
		}
		return err
	}
	return nil
}

// Recover drives every pending journaled operation — this package's and the
// lease package's — to a terminal state: prepared handoff/attach operations
// roll forward to convergence, pending ones never ran a side effect and
// close as rolled back.
func Recover(ctx context.Context, db *store.DB) error {
	ops := operation.NewManager(db)
	lmg := lease.NewManager(db, ops) // registers the lease effect kinds
	registerEffects(ops, db)
	return lmg.Recover(ctx)
}

// mustNewToken mints a fresh correlation token.
func mustNewToken() identity.Token {
	token, err := identity.NewToken()
	if err != nil {
		panic(fmt.Sprintf("handoff: mint token: %v", err))
	}
	return token
}

// writeAtomicFile writes data at path through securefs (atomic, fsynced,
// symlink-refusing), creating parent directories as needed.
func writeAtomicFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("handoff: mkdir %s: %w", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(path), data, mode); err != nil {
		return fmt.Errorf("handoff: write %s: %w", path, err)
	}
	return nil
}
