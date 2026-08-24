package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ConfigPath returns the workspace manifest path under a control root.
func ConfigPath(controlRoot string) string {
	return filepath.Join(controlRoot, ".homonto", "config.toml")
}

// IntegrationsDir returns the per-work integration directory for one member
// under a control root. This task's "unintegrated assignments" check is the
// existence of this directory; Task 3 owns the real branch state inside it.
func IntegrationsDir(controlRoot string, workID identity.WorkID, repoID identity.RepositoryID) string {
	return filepath.Join(controlRoot, ".homonto", "integrations", string(workID), string(repoID))
}

// Typed rescan errors. Wrap with context via fmt.Errorf("%w", ...).
var (
	// ErrNotActiveWork: RescanActive found no checkpoint commit marker for
	// the work, so there is no active work to change membership of.
	ErrNotActiveWork = errors.New("workspace: no active work sentinel for this work")
	// ErrUnintegratedAssignments: a removed member still has unintegrated
	// assignments under .homonto/integrations.
	ErrUnintegratedAssignments = errors.New("workspace: member removal blocked by unintegrated assignments")
	// ErrRemovedMemberNotLeased: the active lease set does not record a
	// lease for a removed member.
	ErrRemovedMemberNotLeased = errors.New("workspace: removed member has no active lease")
	// ErrMemberUnusable: an added member is not usable on disk.
	ErrMemberUnusable = errors.New("workspace: member not usable on disk")
	// ErrInvalidRescanRequest: a RescanRequest is not valid.
	ErrInvalidRescanRequest = errors.New("workspace: invalid rescan request")
)

// Service performs workspace operations that need the runtime journal and
// the lease machinery: membership changes during active work.
type Service struct {
	DB        *store.DB
	Ops       *operation.Manager
	StateRoot string
	// Git inspects git members; nil means gitx.ExecRunner{}.
	Git gitx.Runner
}

// NewService returns a Service journaling through db and ops, with non-git
// member slots under stateRoot (the platform state base). The rescan effect
// kinds are registered immediately so in-process cleanup and recovery can
// dispatch them.
func NewService(db *store.DB, ops *operation.Manager, stateRoot string, git gitx.Runner) *Service {
	s := &Service{DB: db, Ops: ops, StateRoot: stateRoot, Git: git}
	s.registerEffects()
	return s
}

// RescanRequest names the workspace, the active work, and the membership
// change: OldConfig is the committed manifest, NewConfig the target state.
// Config activation only happens through this operation.
type RescanRequest struct {
	WorkspaceID   identity.WorkspaceID
	WorkID        identity.WorkID
	Generation    uint64
	ControlRoot   string
	WorkspaceRoot string
	OldConfig     workspacecfg.Config
	NewConfig     workspacecfg.Config
}

// Validate checks the request in canonical form.
func (r RescanRequest) Validate() error {
	if err := identity.ValidateUUID(string(r.WorkspaceID)); err != nil {
		return fmt.Errorf("workspace: rescan workspace_id: %v: %w", err, ErrInvalidRescanRequest)
	}
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("workspace: rescan work_id: %v: %w", err, ErrInvalidRescanRequest)
	}
	if r.Generation == 0 {
		return fmt.Errorf("workspace: rescan generation must be at least 1: %w", ErrInvalidRescanRequest)
	}
	for field, path := range map[string]string{"control_root": r.ControlRoot, "workspace_root": r.WorkspaceRoot} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace: rescan %s %q must be clean and absolute: %w", field, path, ErrInvalidRescanRequest)
		}
	}
	if err := workspacecfg.Validate(r.WorkspaceRoot, r.OldConfig); err != nil {
		return fmt.Errorf("workspace: rescan old config: %w", err)
	}
	if err := workspacecfg.Validate(r.WorkspaceRoot, r.NewConfig); err != nil {
		return fmt.Errorf("workspace: rescan new config: %w", err)
	}
	return nil
}

// RescanActive applies a membership change during active work as one
// journaled operation:
//
//   - adding a member claims its registration and acquires its lease BEFORE
//     the config activation; failure rolls both back,
//   - removing a member is blocked while it has unintegrated assignments
//     (this task: any directory under .homonto/integrations/<work>/<repo>),
//   - the committed membership update invalidates downstream evidence: the
//     rescan is journaled and the checkpoint commit marker's version is
//     bumped (engine-side evidence invalidation is the workflow workstreams'
//     job),
//   - a removed member's lease is released only after the committed
//     membership update.
//
// Crash recovery rolls the operation forward (membership converges); an
// in-process failure rolls it back, restoring the config, the sentinel
// version, and the released leases exactly.
func (s *Service) RescanActive(ctx context.Context, req RescanRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	oldBytes, err := s.readFile(ConfigPath(req.ControlRoot))
	if err != nil {
		return fmt.Errorf("workspace: read config: %w", err)
	}
	newBytes, err := workspacecfg.Marshal(req.NewConfig)
	if err != nil {
		return fmt.Errorf("workspace: marshal config: %w", err)
	}
	if string(oldBytes) == string(newBytes) {
		return nil
	}

	// The membership change applies only to an active work: the checkpoint
	// commit marker must already be in place.
	sentinelPath := lease.SentinelPath(req.ControlRoot, req.WorkID)
	sentinel, err := lease.ReadSentinel(sentinelPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("workspace: rescan: %w", ErrNotActiveWork)
		}
		return fmt.Errorf("workspace: rescan: read commit marker: %w", err)
	}
	if sentinel.WorkspaceID != req.WorkspaceID {
		return fmt.Errorf("workspace: rescan: commit marker names workspace %s, not %s: %w",
			sentinel.WorkspaceID, req.WorkspaceID, ErrNotActiveWork)
	}

	added, removed := membershipDelta(req.OldConfig, req.NewConfig)

	// Removed members with unintegrated assignments block the whole change
	// before anything is journaled.
	byID := memberByID(sentinel.Leases)
	for _, m := range removed {
		if _, ok := byID[m.ID]; !ok {
			return fmt.Errorf("workspace: rescan: member %s (%s) has no active lease: %w",
				m.ID, m.Path, ErrRemovedMemberNotLeased)
		}
		if _, err := os.Stat(IntegrationsDir(req.ControlRoot, req.WorkID, m.ID)); err == nil {
			return fmt.Errorf("workspace: rescan: member %s (%s) has unintegrated assignments: %w",
				m.ID, m.Path, ErrUnintegratedAssignments)
		}
	}

	runner := s.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("workspace: rescan: generate operation id: %w", err)
	}
	added = append([]workspacecfg.Member(nil), added...)
	sort.Slice(added, func(i, j int) bool { return added[i].ID < added[j].ID })
	payload := rescanPayload{
		WorkspaceID: req.WorkspaceID, WorkID: req.WorkID, Generation: req.Generation,
		ControlRoot: req.ControlRoot,
	}
	for _, m := range added {
		payload.Added = append(payload.Added, m.ID)
	}
	for _, m := range removed {
		payload.Removed = append(payload.Removed, m.ID)
	}
	var effects []operation.Effect
	for _, m := range added {
		regPath, leasePath, err := s.memberSlots(ctx, runner, req, m)
		if err != nil {
			return err
		}
		reg := registration.Registration{
			SchemaVersion: 1,
			WorkspaceID:   req.WorkspaceID,
			RepositoryID:  m.ID,
			ControlRoot:   req.ControlRoot,
			MemberRoot:    filepath.Join(req.WorkspaceRoot, filepath.FromSlash(m.Path)),
			Kind:          m.Kind,
		}
		content, err := leaseContentFor(req, m.ID)
		if err != nil {
			return err
		}
		effects = append(effects, &claimEffect{payload: claimPayload{Path: regPath, Registration: reg}})
		effects = append(effects, lease.NewCreateLeaseEffect(leasePath, content))
	}

	effects = append(effects, &writeConfigEffect{payload: writeConfigPayload{
		Path: ConfigPath(req.ControlRoot), Data: newBytes, Previous: oldBytes,
	}})
	bumped := sentinel
	bumped.Version++
	effects = append(effects, &sentinelBumpEffect{payload: sentinelBumpPayload{
		Path: sentinelPath, Content: bumped, Previous: sentinel,
	}})

	removed = append([]workspacecfg.Member(nil), removed...)
	sort.Slice(removed, func(i, j int) bool { return removed[i].ID < removed[j].ID })
	for _, m := range removed {
		leasePath := byID[m.ID].Path
		content, err := lease.ReadLease(leasePath)
		if err != nil {
			return fmt.Errorf("workspace: rescan: read lease of %s (%s): %w", m.ID, m.Path, err)
		}
		effects = append(effects, lease.NewRemoveLeaseEffect(leasePath, content))
	}

	op := &rescanOperation{
		id: opID, workID: req.WorkID, generation: req.Generation,
		payload: payload,
		effects: effects,
	}
	if err := s.Ops.Run(ctx, op); err != nil {
		// In-process failure: undo the partial membership change, restoring
		// the config, the sentinel version, and the released leases.
		cleanupErr := s.DB.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationPolicy(ctx, opID, string(operation.RollBack))
		})
		if cleanupErr == nil {
			cleanupErr = s.Ops.RecoverOne(ctx, opID)
		}
		if cleanupErr != nil {
			return fmt.Errorf("workspace: rescan %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return fmt.Errorf("workspace: rescan %s: %w", opID, err)
	}
	return nil
}

// Recover registers the rescan effect kinds with the operation manager so
// recovery can dispatch a pending rescan operation. Call it before (or after
// — registration is idempotent) the lease manager's Recover; the recovery
// pass itself converges a crashed rescan by rolling it forward.
func (s *Service) Recover(ctx context.Context) error {
	s.registerEffects()
	return s.Ops.RecoverPending(ctx)
}

// registerEffects installs every rescan effect prototype for recovery
// dispatch. Registration is idempotent.
func (s *Service) registerEffects() {
	s.Ops.RegisterEffect(&claimEffect{})
	s.Ops.RegisterEffect(&writeConfigEffect{})
	s.Ops.RegisterEffect(&sentinelBumpEffect{})
}

// memberSlots derives the registration and lease paths for an added member
// from its kind: git members live in the git common directory (runner
// derived), non-git members under the state root keyed by canonical path.
func (s *Service) memberSlots(ctx context.Context, runner gitx.Runner, req RescanRequest, m workspacecfg.Member) (regPath, leasePath string, err error) {
	memberRoot := filepath.Join(req.WorkspaceRoot, filepath.FromSlash(m.Path))
	info, serr := os.Stat(memberRoot)
	if serr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("workspace: rescan: member %s (%s) is not a directory: %w",
			m.ID, m.Path, ErrMemberUnusable)
	}
	if m.Kind == workspacecfg.KindGit {
		repo, isGit, ierr := gitx.Inspect(ctx, runner, memberRoot)
		if ierr != nil {
			return "", "", fmt.Errorf("workspace: rescan: inspect git member %s (%s): %w", m.ID, m.Path, ierr)
		}
		if !isGit || repo.TopLevel != memberRoot {
			return "", "", fmt.Errorf("workspace: rescan: member %s (%s) declared git but %s is not a repository root: %w",
				m.ID, m.Path, memberRoot, ErrMemberUnusable)
		}
		return registration.GitRegistrationPath(repo.CommonDir), registration.GitLeasePath(repo.CommonDir), nil
	}
	regPath, err = registration.NonGitRegistrationPath(s.StateRoot, memberRoot)
	if err != nil {
		return "", "", err
	}
	leasePath, err = registration.NonGitLeasePath(s.StateRoot, memberRoot)
	if err != nil {
		return "", "", err
	}
	return regPath, leasePath, nil
}

// membershipDelta splits the membership change into added and removed
// members by repository id.
func membershipDelta(oldCfg, newCfg workspacecfg.Config) (added, removed []workspacecfg.Member) {
	oldByID := map[identity.RepositoryID]workspacecfg.Member{}
	newByID := map[identity.RepositoryID]workspacecfg.Member{}
	for _, m := range oldCfg.Members {
		oldByID[m.ID] = m
	}
	for _, m := range newCfg.Members {
		newByID[m.ID] = m
	}
	for id, m := range newByID {
		if _, ok := oldByID[id]; !ok {
			added = append(added, m)
		}
	}
	for id, m := range oldByID {
		if _, ok := newByID[id]; !ok {
			removed = append(removed, m)
		}
	}
	return added, removed
}

// memberByID indexes the sentinel's lease list by repository id.
func memberByID(leases []lease.SentinelLease) map[identity.RepositoryID]lease.SentinelLease {
	out := make(map[identity.RepositoryID]lease.SentinelLease, len(leases))
	for _, l := range leases {
		out[l.RepositoryID] = l
	}
	return out
}

// leaseContentFor builds the lease content for a newly added member.
func leaseContentFor(req RescanRequest, repoID identity.RepositoryID) (lease.LeaseContent, error) {
	token, err := identity.NewToken()
	if err != nil {
		return lease.LeaseContent{}, fmt.Errorf("workspace: rescan: generate token: %w", err)
	}
	prov, err := lease.CurrentProcess()
	if err != nil {
		return lease.LeaseContent{}, err
	}
	return lease.LeaseContent{
		SchemaVersion: 1,
		WorkspaceID:   req.WorkspaceID,
		RepositoryID:  repoID,
		WorkID:        req.WorkID,
		Generation:    req.Generation,
		Process:       prov,
		RecoveryToken: token,
	}, nil
}

// readFile reads one control-plane file through securefs.
func (s *Service) readFile(path string) ([]byte, error) {
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(filepath.Base(path))
}

// rescanPayload is the operation payload of a membership change: the
// identity plus the journaled delta, so the journal records the change
// itself and not only its effects.
type rescanPayload struct {
	WorkspaceID identity.WorkspaceID    `json:"workspace_id"`
	WorkID      identity.WorkID         `json:"work_id"`
	Generation  uint64                  `json:"generation"`
	ControlRoot string                  `json:"control_root"`
	Added       []identity.RepositoryID `json:"added,omitempty"`
	Removed     []identity.RepositoryID `json:"removed,omitempty"`
}

// rescanOperation is the journaled membership change.
type rescanOperation struct {
	id         identity.OperationID
	workID     identity.WorkID
	generation uint64
	payload    rescanPayload
	effects    []operation.Effect
}

func (o *rescanOperation) ID() identity.OperationID    { return o.id }
func (o *rescanOperation) Kind() string                { return "lease.rescan" }
func (o *rescanOperation) WorkID() identity.WorkID     { return o.workID }
func (o *rescanOperation) Generation() int64           { return int64(o.generation) }
func (o *rescanOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *rescanOperation) Payload() any                { return o.payload }
func (o *rescanOperation) Effects() []operation.Effect { return o.effects }

// claimPayload is the journalled identity of one registration claim.
type claimPayload struct {
	Path         string                    `json:"path"`
	Registration registration.Registration `json:"registration"`
}

// claimEffect claims an added member's registration slot. Apply reuses
// registration.Claim's O_EXCL semantics and no-ops when the slot already
// holds exactly this registration (idempotent re-apply); Revert removes only
// the workspace's own claim.
type claimEffect struct {
	payload claimPayload
}

func (e *claimEffect) Kind() string { return "ws.claim" }

func (e *claimEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *claimEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p claimPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode claim payload of effect %d: %w", rec.Seq, err)
	}
	if err := registration.Claim(p.Path, p.Registration); err != nil {
		if errors.Is(err, registration.ErrOwnedByOther) {
			existing, rerr := registration.Read(p.Path)
			if rerr == nil && existing == p.Registration {
				return nil // unrecorded-window re-apply: we already claimed it
			}
		}
		return fmt.Errorf("workspace: claim %s: %w", p.Path, err)
	}
	return nil
}

func (e *claimEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p claimPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode claim payload of effect %d: %w", rec.Seq, err)
	}
	if err := registration.Detach(p.Path, p.Registration.WorkspaceID); err != nil {
		if errors.Is(err, registration.ErrNotRegistered) {
			return nil // idempotent revert: nothing to detach
		}
		return fmt.Errorf("workspace: detach claim %s: %w", p.Path, err)
	}
	return nil
}

// writeConfigPayload is the journalled identity of the config activation.
type writeConfigPayload struct {
	Path     string `json:"path"`
	Data     []byte `json:"data"`
	Previous []byte `json:"previous"`
}

// writeConfigEffect activates the new manifest atomically. Revert restores
// the previous bytes.
type writeConfigEffect struct {
	payload writeConfigPayload
}

func (e *writeConfigEffect) Kind() string { return "ws.write_config" }

func (e *writeConfigEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *writeConfigEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p writeConfigPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode config payload of effect %d: %w", rec.Seq, err)
	}
	return writeConfigFile(p.Path, p.Data)
}

func (e *writeConfigEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p writeConfigPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode config payload of effect %d: %w", rec.Seq, err)
	}
	return writeConfigFile(p.Path, p.Previous)
}

// writeConfigFile atomically replaces the manifest at path.
func writeConfigFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(path), data, 0o644); err != nil {
		return fmt.Errorf("workspace: write config %s: %w", path, err)
	}
	return nil
}

// sentinelBumpPayload is the journalled identity of one evidence-staleness
// bump of the checkpoint commit marker.
type sentinelBumpPayload struct {
	Path     string                `json:"path"`
	Content  lease.SentinelContent `json:"content"`
	Previous lease.SentinelContent `json:"previous"`
}

// sentinelBumpEffect marks the checkpoint commit marker stale by bumping its
// version, invalidating downstream evidence until it is re-derived. Revert
// restores the previous version; the marker itself is never removed.
type sentinelBumpEffect struct {
	payload sentinelBumpPayload
}

func (e *sentinelBumpEffect) Kind() string { return "ws.sentinel_bump" }

func (e *sentinelBumpEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *sentinelBumpEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelBumpPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode sentinel bump payload of effect %d: %w", rec.Seq, err)
	}
	return writeSentinel(p.Path, p.Content)
}

func (e *sentinelBumpEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p sentinelBumpPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("workspace: decode sentinel bump payload of effect %d: %w", rec.Seq, err)
	}
	return writeSentinel(p.Path, p.Previous)
}

// writeSentinel atomically replaces the checkpoint commit marker.
func writeSentinel(path string, content lease.SentinelContent) error {
	data, err := content.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workspace: mkdir %s: %w", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(path), data, 0o600); err != nil {
		return fmt.Errorf("workspace: write commit marker %s: %w", path, err)
	}
	return nil
}
