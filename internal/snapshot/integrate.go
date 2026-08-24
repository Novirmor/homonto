package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// Operation kinds journaled by this package.
const (
	// OpKindCreateAssignment names a non-Git assignment creation
	// (snapshot capture + work-tree materialization).
	OpKindCreateAssignment = "snapshot.assignment.create"
	// OpKindApplyToStage names one patch applied to the integration
	// stage.
	OpKindApplyToStage = "snapshot.stage.apply"
)

// Effect kinds registered for recovery.
const (
	kindSnapshotCapture  = "snapshot.capture"
	kindSnapshotMaterial = "snapshot.materialize-work"
	kindStageApply       = "snapshot.stage-apply"
)

// Path derivations inside one member's store root
// (<root>/.homonto/integrations/<work-id>/<repository-id>):
//
//	snapshots/<action-id>/base/manifest.json   the base snapshot
//	snapshots/<action-id>/work/                the implementer's tree
//	patches/<action-id>/manifest.json          the result patch
//	blobs/sha256/<digest>                      content-addressed blobs
//	stage/                                     the integration stage
func BaseManifestPath(storePath string, actionID identity.ActionID) string {
	return filepath.Join(storePath, "snapshots", string(actionID), "base", "manifest.json")
}

func WorkTreePath(storePath string, actionID identity.ActionID) string {
	return filepath.Join(storePath, "snapshots", string(actionID), "work")
}

func PatchManifestPath(storePath string, actionID identity.ActionID) string {
	return filepath.Join(storePath, "patches", string(actionID), "manifest.json")
}

func StageTreePath(storePath string) string {
	return filepath.Join(storePath, "stage")
}

// Service orchestrates the journaled snapshot workflow for non-Git
// members: assignment creation (capture + work tree), result diffing,
// and staged integration. It is safe for concurrent use: the store
// serializes journal transitions and every effect is idempotent.
type Service struct {
	db    *store.DB
	ops   *operation.Manager
	store string
}

// NewService returns a Service journaling through db and ops and placing
// snapshots, patches, blobs, and the stage under storePath. The snapshot
// effect kinds are registered immediately so both in-process cleanup and
// recovery can dispatch them.
func NewService(db *store.DB, ops *operation.Manager, storePath string) (*Service, error) {
	if db == nil || ops == nil {
		return nil, fmt.Errorf("snapshot: db and operation managers are required")
	}
	if err := validateAbsDir("store", storePath); err != nil {
		return nil, err
	}
	s := &Service{db: db, ops: ops, store: storePath}
	s.registerEffects()
	return s, nil
}

func (s *Service) registerEffects() {
	s.ops.RegisterEffect(&captureEffect{})
	s.ops.RegisterEffect(&materializeWorkEffect{})
	s.ops.RegisterEffect(&stageApplyEffect{})
}

// StagePath returns the integration stage directory.
func (s *Service) StagePath() string { return StageTreePath(s.store) }

// Assignment names one implementer's isolated non-Git workspace.
type Assignment struct {
	WorkID       identity.WorkID
	ActionID     identity.ActionID
	RepositoryID identity.RepositoryID
	// ManifestPath is the base snapshot's manifest file.
	ManifestPath string
	// WorkPath is the implementer's editable tree (materialized from the
	// base snapshot).
	WorkPath string
	// BaseDigest is the base snapshot's root digest.
	BaseDigest fingerprint.Digest
}

// AssignmentRequest names one non-Git assignment.
type AssignmentRequest struct {
	WorkID       identity.WorkID
	ActionID     identity.ActionID
	RepositoryID identity.RepositoryID
	SourceDir    string
	// Exclusions are capture patterns (see CaptureOptions).
	Exclusions []string
}

// Validate checks the request in canonical form.
func (r AssignmentRequest) Validate() error {
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("snapshot: work_id: %v", err)
	}
	if err := identity.ValidateUUID(string(r.ActionID)); err != nil {
		return fmt.Errorf("snapshot: action_id: %v", err)
	}
	if err := identity.ValidateUUID(string(r.RepositoryID)); err != nil {
		return fmt.Errorf("snapshot: repository_id: %v", err)
	}
	if err := validateAbsDir("source", r.SourceDir); err != nil {
		return err
	}
	if _, err := compileExclusions(r.Exclusions); err != nil {
		return err
	}
	return nil
}

// capturePayload is what the capture effect persists: everything replay
// needs to re-capture identically.
type capturePayload struct {
	RepositoryID identity.RepositoryID `json:"repository_id"`
	SourceDir    string                `json:"source_dir"`
	Store        string                `json:"store"`
	ManifestPath string                `json:"manifest_path"`
	Exclusions   []string              `json:"exclusions,omitempty"`
}

// materializeWorkPayload is what the work-tree effect persists.
type materializeWorkPayload struct {
	ManifestPath string `json:"manifest_path"`
	Store        string `json:"store"`
	WorkPath     string `json:"work_path"`
}

// stageApplyPayload is what the stage-apply effect persists.
type stageApplyPayload struct {
	StageDir         string `json:"stage_dir"`
	BlobDir          string `json:"blob_dir"`
	PatchPath        string `json:"patch_path"`
	BaseManifestPath string `json:"base_manifest_path"`
	Store            string `json:"store"`
}

// CreateAssignment captures the member source tree into an immutable
// snapshot (blobs + base manifest under snapshots/<action-id>/) and
// materializes the implementer's work tree from it — the non-Git mirror
// of a git assignment worktree. Two journaled effects run under
// RollForward: the capture (idempotent by content addressing) and the
// work-tree materialization (idempotent by verify-or-rebuild). Reverting
// removes the work tree and the manifest; blobs are shared,
// content-addressed state and are deliberately never deleted.
//
// A recovery that finds the source changed since the first capture fails
// closed (the recorded base is the truth; a re-capture of different
// content never overwrites it).
func (s *Service) CreateAssignment(ctx context.Context, req AssignmentRequest) (Assignment, error) {
	if err := req.Validate(); err != nil {
		return Assignment{}, err
	}
	manifestPath := BaseManifestPath(s.store, req.ActionID)
	workPath := WorkTreePath(s.store, req.ActionID)
	opID, err := identity.NewOperationID()
	if err != nil {
		return Assignment{}, fmt.Errorf("snapshot: generate operation id: %w", err)
	}
	op := &assignmentCreateOperation{
		id:     opID,
		workID: req.WorkID,
		payload: map[string]any{
			"action_id": string(req.ActionID), "source_dir": req.SourceDir,
			"manifest_path": manifestPath, "work_path": workPath,
		},
		effects: []operation.Effect{
			&captureEffect{payload: capturePayload{
				RepositoryID: req.RepositoryID,
				SourceDir:    req.SourceDir,
				Store:        s.store,
				ManifestPath: manifestPath,
				Exclusions:   req.Exclusions,
			}},
			&materializeWorkEffect{payload: materializeWorkPayload{
				ManifestPath: manifestPath, Store: s.store, WorkPath: workPath,
			}},
		},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		if cleanupErr := s.finishOrRollBack(ctx, opID); cleanupErr != nil {
			return Assignment{}, fmt.Errorf("snapshot: create assignment %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return Assignment{}, fmt.Errorf("snapshot: create assignment %s: %w", opID, err)
	}
	manifest, err := readManifestFile(manifestPath)
	if err != nil {
		return Assignment{}, err
	}
	return Assignment{
		WorkID:       req.WorkID,
		ActionID:     req.ActionID,
		RepositoryID: req.RepositoryID,
		ManifestPath: manifestPath,
		WorkPath:     workPath,
		BaseDigest:   manifest.RootDigest,
	}, nil
}

// DiffResult diffs the assignment's work tree against its base snapshot
// and writes the patch to patches/<action-id>/manifest.json, returning
// it. This is not journaled: the diff is a pure function of the work
// tree's current state plus idempotent, atomic file writes, so an
// interrupted run simply recomputes. Scope enforcement of the produced
// patch is the engine's call (ValidateScope).
func (s *Service) DiffResult(ctx context.Context, a Assignment) (PatchManifest, error) {
	base, err := readManifestFile(a.ManifestPath)
	if err != nil {
		return PatchManifest{}, err
	}
	patch, err := Diff(ctx, base, a.WorkPath, BlobDir(s.store))
	if err != nil {
		return PatchManifest{}, err
	}
	patchPath := PatchManifestPath(s.store, a.ActionID)
	if err := writeJSONFile(patchPath, func() ([]byte, error) { return EncodePatch(patch) }); err != nil {
		return PatchManifest{}, err
	}
	return patch, nil
}

// applyConfig carries the options of one ApplyToStage run.
type applyConfig struct {
	terminal bool
	prior    []Assignment
}

// ApplyOption configures one ApplyToStage run.
type ApplyOption func(*applyConfig)

// WithTerminalVerify marks this apply as the LAST of a sequential
// integration: after the journaled apply finalizes, the stage is verified
// against the cumulative expected state — the first listed assignment's
// (or, with none listed, this one's own) base manifest plus every prior
// patch and this one, in order. This is the terminal verification a
// multi-material stage needs (no single patch's result digest covers a
// stage carrying earlier materials). The verification is read-only: a
// typed failure (ErrVerifyFailed naming the path) reverts nothing — the
// journaled applies are already final — it is the engine's signal that
// the stage diverged.
func WithTerminalVerify(prior ...Assignment) ApplyOption {
	return func(c *applyConfig) { c.terminal = true; c.prior = prior }
}

// ApplyToStage applies the assignment's patch to the integration stage,
// journaled as one RollForward effect. An empty or missing stage is
// seeded from the assignment's base snapshot first (correct for the
// first patch; the engine applies patches in dependency order, mirroring
// cherry-pick sequencing). The effect's Apply is idempotent — per-op
// preimage and already-applied checks converge a partial stage — and its
// Revert rolls the patch back through its inverse operations, so
// earlier materials applied to the stage survive.
//
// The journaled effect verifies the whole-stage digest only when it
// seeded the stage itself (single-material case); sequential applies are
// guarded per-op and the engine terminates a sequence with
// WithTerminalVerify for the cumulative check.
func (s *Service) ApplyToStage(ctx context.Context, a Assignment, opts ...ApplyOption) error {
	var cfg applyConfig
	for _, o := range opts {
		o(&cfg)
	}
	patchPath := PatchManifestPath(s.store, a.ActionID)
	if _, err := os.Stat(patchPath); err != nil {
		return fmt.Errorf("snapshot: apply to stage: patch of action %s: %w", a.ActionID, err)
	}
	payload := stageApplyPayload{
		StageDir:         s.StagePath(),
		BlobDir:          BlobDir(s.store),
		PatchPath:        patchPath,
		BaseManifestPath: a.ManifestPath,
		Store:            s.store,
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("snapshot: generate operation id: %w", err)
	}
	op := &stageApplyOperation{
		id:      opID,
		workID:  a.WorkID,
		payload: payload,
		effects: []operation.Effect{&stageApplyEffect{payload: payload}},
	}
	if err := s.ops.Run(ctx, op); err != nil {
		if cleanupErr := s.finishOrRollBack(ctx, opID); cleanupErr != nil {
			return fmt.Errorf("snapshot: apply to stage %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return fmt.Errorf("snapshot: apply to stage %s: %w", opID, err)
	}
	if cfg.terminal {
		return s.verifyStageFrom(ctx, cfg.prior, a)
	}
	return nil
}

// verifyStageFrom runs the terminal stage verification: the base is the
// seed assignment's base manifest (the first prior, else the last apply's
// own), the patches are the prior sequence followed by the last apply.
func (s *Service) verifyStageFrom(ctx context.Context, prior []Assignment, last Assignment) error {
	seed := last
	if len(prior) > 0 {
		seed = prior[0]
	}
	base, err := readManifestFile(seed.ManifestPath)
	if err != nil {
		return fmt.Errorf("snapshot: terminal verify: %w", err)
	}
	sequence := make([]Assignment, 0, len(prior)+1)
	sequence = append(sequence, prior...)
	sequence = append(sequence, last)
	patches := make([]PatchManifest, 0, len(sequence))
	for _, as := range sequence {
		patch, err := readPatchFile(PatchManifestPath(s.store, as.ActionID))
		if err != nil {
			return fmt.Errorf("snapshot: terminal verify: %w", err)
		}
		patches = append(patches, patch)
	}
	return VerifyStage(ctx, base, patches, s.StagePath())
}

// Recover drives every pending journaled operation of this store to a
// terminal state (ADR 0025 recovery).
func (s *Service) Recover(ctx context.Context) error {
	return s.ops.RecoverPending(ctx)
}

// finishOrRollBack switches a failed operation to roll-back and drives
// it to a terminal state, mirroring gitx's cleanup posture: an effect
// that already ran must be reverted, not re-applied.
func (s *Service) finishOrRollBack(ctx context.Context, opID identity.OperationID) error {
	rec, err := s.db.Operation(ctx, opID)
	if err != nil {
		return fmt.Errorf("snapshot: cleanup: %w", err)
	}
	switch rec.State {
	case store.OpFinalized, store.OpRolledBack:
		return nil
	}
	if err := s.db.Update(ctx, func(tx *store.Tx) error {
		return tx.SetOperationPolicy(ctx, opID, string(operation.RollBack))
	}); err != nil {
		return fmt.Errorf("snapshot: cleanup: switch %s to roll-back: %w", opID, err)
	}
	return s.ops.RecoverOne(ctx, opID)
}

// assignmentCreateOperation is the journaled assignment creation.
type assignmentCreateOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	payload any
	effects []operation.Effect
}

func (o *assignmentCreateOperation) ID() identity.OperationID    { return o.id }
func (o *assignmentCreateOperation) Kind() string                { return OpKindCreateAssignment }
func (o *assignmentCreateOperation) WorkID() identity.WorkID     { return o.workID }
func (o *assignmentCreateOperation) Generation() int64           { return 0 }
func (o *assignmentCreateOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *assignmentCreateOperation) Payload() any                { return o.payload }
func (o *assignmentCreateOperation) Effects() []operation.Effect { return o.effects }

// stageApplyOperation is the journaled stage application. The policy is
// a field so tests (and only tests) can exercise the roll-back path;
// the service always issues RollForward.
type stageApplyOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	policy  operation.Policy
	payload stageApplyPayload
	effects []operation.Effect
}

func (o *stageApplyOperation) ID() identity.OperationID { return o.id }
func (o *stageApplyOperation) Kind() string             { return OpKindApplyToStage }
func (o *stageApplyOperation) WorkID() identity.WorkID  { return o.workID }
func (o *stageApplyOperation) Generation() int64        { return 0 }
func (o *stageApplyOperation) Policy() operation.Policy {
	if o.policy == "" {
		return operation.RollForward
	}
	return o.policy
}
func (o *stageApplyOperation) Payload() any                { return o.payload }
func (o *stageApplyOperation) Effects() []operation.Effect { return o.effects }

// captureEffect captures the source and installs the base manifest.
// Apply is idempotent: blobs are content-addressed, and an existing
// manifest with the same root digest is already applied — a manifest
// with a DIFFERENT digest means the source changed under recovery and
// fails closed. Revert removes the manifest only: blobs are shared
// content-addressed state and are never deleted by rollback.
type captureEffect struct {
	payload capturePayload
}

func (e *captureEffect) Kind() string { return kindSnapshotCapture }

func (e *captureEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *captureEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[capturePayload](rec)
	if err != nil {
		return err
	}
	m, err := Capture(ctx, p.SourceDir, p.Store, CaptureOptions{Exclusions: p.Exclusions})
	if err != nil {
		return err
	}
	m.RepositoryID = p.RepositoryID // not covered by RootDigest (tree-only)
	if existing, err := readManifestFile(p.ManifestPath); err == nil {
		if existing.RootDigest != m.RootDigest {
			return fmt.Errorf("snapshot: source of assignment changed under recovery: recorded base %s, recaptured %s: %w",
				existing.RootDigest, m.RootDigest, ErrDigestMismatch)
		}
		return nil // already applied
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return writeJSONFile(p.ManifestPath, func() ([]byte, error) { return EncodeManifest(m) })
}

func (e *captureEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[capturePayload](rec)
	if err != nil {
		return err
	}
	if err := os.Remove(p.ManifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("snapshot: revert capture: remove manifest: %w", err)
	}
	return nil
}

// materializeWorkEffect materializes (or re-converges) the implementer's
// work tree from the base manifest. Revert removes the work tree.
type materializeWorkEffect struct {
	payload materializeWorkPayload
}

func (e *materializeWorkEffect) Kind() string { return kindSnapshotMaterial }

func (e *materializeWorkEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *materializeWorkEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[materializeWorkPayload](rec)
	if err != nil {
		return err
	}
	manifest, err := readManifestFile(p.ManifestPath)
	if err != nil {
		return err
	}
	return EnsureMaterialized(ctx, manifest, p.Store, p.WorkPath)
}

func (e *materializeWorkEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[materializeWorkPayload](rec)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p.WorkPath); err != nil {
		return fmt.Errorf("snapshot: revert work tree: %w", err)
	}
	return nil
}

// stageApplyEffect seeds an empty stage from the base snapshot and
// applies the patch. Apply is idempotent through applyPatch's per-op
// already-applied/preimage logic plus the final result-digest verify;
// Revert applies the patch's inverse operations (skipping its own final
// verify — the pre-patch stage of a multi-material integration has no
// manifest to verify against), preserving earlier materials.
type stageApplyEffect struct {
	payload stageApplyPayload
}

func (e *stageApplyEffect) Kind() string { return kindStageApply }

func (e *stageApplyEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *stageApplyEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[stageApplyPayload](rec)
	if err != nil {
		return err
	}
	patch, err := readPatchFile(p.PatchPath)
	if err != nil {
		return err
	}
	// Seed an empty stage from this patch's base snapshot; a non-empty
	// stage (earlier materials or a partial apply) is never reset.
	seeded := false
	switch empty, err := stageDirEmpty(p.StageDir); {
	case err != nil:
		return err
	case empty:
		seeded = true
		base, err := readManifestFile(p.BaseManifestPath)
		if err != nil {
			return err
		}
		if err := Materialize(ctx, base, p.Store, p.StageDir); err != nil {
			return fmt.Errorf("snapshot: seed stage: %w", err)
		}
	}
	// The whole-stage digest check holds exactly when this patch seeded
	// the stage (single-material case): a stage carrying earlier
	// materials can never equal one patch's result digest, and a
	// partially-applied stage re-run after a crash is verified through
	// per-op preimages instead.
	return applyPatch(ctx, p.StageDir, p.BlobDir, patch, seeded)
}

func (e *stageApplyEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	p, err := decodePayload[stageApplyPayload](rec)
	if err != nil {
		return err
	}
	patch, err := readPatchFile(p.PatchPath)
	if err != nil {
		return err
	}
	inverse, err := InvertPatch(patch)
	if err != nil {
		return err
	}
	return applyPatch(ctx, p.StageDir, p.BlobDir, inverse, false)
}

// stageDirEmpty reports whether the stage directory is missing or holds
// nothing.
func stageDirEmpty(stage string) (bool, error) {
	entries, err := os.ReadDir(stage)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("snapshot: read stage %s: %w", stage, err)
	}
	return len(entries) == 0, nil
}

// decodePayload strict-decodes an effect record's persisted payload.
func decodePayload[T any](rec operation.EffectRecord) (T, error) {
	var into T
	if err := json.Unmarshal(rec.Payload, &into); err != nil {
		return into, fmt.Errorf("snapshot: decode %s payload: %w", rec.Kind, err)
	}
	return into, nil
}

// readManifestFile reads and strict-decodes a manifest file.
func readManifestFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, fmt.Errorf("snapshot: manifest %s: %w", path, err)
		}
		return Manifest{}, fmt.Errorf("snapshot: read manifest %s: %w", path, err)
	}
	m, err := DecodeManifest(data)
	if err != nil {
		return Manifest{}, fmt.Errorf("snapshot: manifest %s: %w", path, err)
	}
	return m, nil
}

// readPatchFile reads and strict-decodes a patch manifest file.
func readPatchFile(path string) (PatchManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PatchManifest{}, fmt.Errorf("snapshot: patch %s: %w", path, err)
	}
	p, err := DecodePatch(data)
	if err != nil {
		return PatchManifest{}, fmt.Errorf("snapshot: patch %s: %w", path, err)
	}
	return p, nil
}

// writeJSONFile atomically installs encoded bytes at path (parents
// 0700, temp file + rename, 0600, fsynced).
func writeJSONFile(path string, encode func() ([]byte, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		return fmt.Errorf("snapshot: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := encode()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), snapshotTempPrefix)
	if err != nil {
		return fmt.Errorf("snapshot: create temp for %s: %w", path, err)
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(blobPerm); err != nil {
		return fmt.Errorf("snapshot: chmod temp for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("snapshot: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("snapshot: fsync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("snapshot: close temp for %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("snapshot: install %s: %w", path, err)
	}
	committed = true
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		dir.Sync()
		dir.Close()
	}
	return nil
}
