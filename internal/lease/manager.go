package lease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// Manager journals and recovers member leases through one runtime database
// and one operation manager. It is safe for concurrent use: the store
// serializes every journal transition and the operation manager's effect
// registry is mutex-guarded.
type Manager struct {
	db  *store.DB
	ops *operation.Manager
}

// NewManager returns a Manager journaling through db and ops. The lease
// effect kinds are registered immediately so both the in-process failure
// cleanup and recovery can dispatch them.
func NewManager(db *store.DB, ops *operation.Manager) *Manager {
	m := &Manager{db: db, ops: ops}
	m.registerEffects()
	return m
}

// acquirePayload is the operation payload of an acquisition: the full
// request echo plus one random recovery token per target. The journaled
// payload IS the recorded token store — ValidateAll and recovery read the
// expected tokens from here, and recovery replays the same tokens.
type acquirePayload struct {
	WorkspaceID identity.WorkspaceID `json:"workspace_id"`
	WorkID      identity.WorkID      `json:"work_id"`
	Generation  uint64               `json:"generation"`
	ControlRoot string               `json:"control_root"`
	Provenance  Process              `json:"process"`
	WorkKind    string               `json:"work_kind"`
	Title       string               `json:"title"`
	Targets     []acquireTarget      `json:"targets"`
}

type acquireTarget struct {
	RepositoryID identity.RepositoryID `json:"repository_id"`
	Path         string                `json:"path"`
	Token        identity.Token        `json:"token"`
}

// acquireOperation is the journaled all-or-none acquisition.
type acquireOperation struct {
	id         identity.OperationID
	workID     identity.WorkID
	generation uint64
	payload    acquirePayload
	effects    []operation.Effect
}

func (o *acquireOperation) ID() identity.OperationID    { return o.id }
func (o *acquireOperation) Kind() string                { return OpKindAcquire }
func (o *acquireOperation) WorkID() identity.WorkID     { return o.workID }
func (o *acquireOperation) Generation() int64           { return int64(o.generation) }
func (o *acquireOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *acquireOperation) Payload() any                { return o.payload }
func (o *acquireOperation) Effects() []operation.Effect { return o.effects }

// AcquireAll journaled all-or-none acquisition of every target's lease, in
// stable repository-id order:
//
//  1. the full target list plus one random token per target is journaled as
//     the operation payload,
//  2. every effect is prepared (each carries its token),
//  3. each lease is created with O_EXCL (content includes the token),
//  4. the checkpoint commit marker is written last, atomically,
//  5. the work's active row and the operation-applied marker commit in one
//     SQLite transaction (the final effect),
//  6. the operation finalizes.
//
// On failure the manager drives the operation to a terminal state exactly as
// crash recovery would: forward when the commit marker exists or the
// remaining leases are acquirable, otherwise back — removing only the leases
// whose content and tokens match the journal.
func (m *Manager) AcquireAll(ctx context.Context, req AcquireRequest) ([]Lease, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	workKind := req.WorkKind
	if workKind == "" {
		workKind = "task"
	}

	targets := make([]Target, len(req.Targets))
	copy(targets, req.Targets)
	sort.Slice(targets, func(i, j int) bool { return targets[i].RepositoryID < targets[j].RepositoryID })

	opID, err := identity.NewOperationID()
	if err != nil {
		return nil, fmt.Errorf("lease: generate operation id: %w", err)
	}

	payload := acquirePayload{
		WorkspaceID: req.WorkspaceID,
		WorkID:      req.WorkID,
		Generation:  req.Generation,
		ControlRoot: req.ControlRoot,
		Provenance:  req.Provenance,
		WorkKind:    workKind,
		Title:       req.Title,
	}
	leases := make([]Lease, 0, len(targets))
	var effects []operation.Effect
	for i, t := range targets {
		token, err := identity.NewToken()
		if err != nil {
			return nil, fmt.Errorf("lease: generate token: %w", err)
		}
		content := LeaseContent{
			SchemaVersion: CurrentSchemaVersion,
			WorkspaceID:   req.WorkspaceID,
			RepositoryID:  t.RepositoryID,
			WorkID:        req.WorkID,
			Generation:    req.Generation,
			Process:       req.Provenance.normalized(),
			RecoveryToken: token,
		}
		payload.Targets = append(payload.Targets, acquireTarget{
			RepositoryID: t.RepositoryID, Path: t.Path, Token: token,
		})
		leases = append(leases, Lease{Path: t.Path, OpID: opID, Seq: int64(i + 1), Content: content})
		effects = append(effects, NewCreateLeaseEffect(t.Path, content))
	}
	sentinel := SentinelContent{
		SchemaVersion: CurrentSchemaVersion,
		WorkspaceID:   req.WorkspaceID,
		WorkID:        req.WorkID,
		Generation:    req.Generation,
		Version:       1,
		OperationID:   opID,
	}
	for _, t := range targets {
		sentinel.Leases = append(sentinel.Leases, SentinelLease{RepositoryID: t.RepositoryID, Path: t.Path})
	}
	effects = append(effects, &sentinelEffect{payload: sentinelPayload{
		Path:    SentinelPath(req.ControlRoot, req.WorkID),
		Content: sentinel,
	}})
	effects = append(effects, &activateEffect{payload: activatePayload{
		WorkID: req.WorkID, OperationID: opID, WorkKind: workKind, Title: req.Title,
	}, db: m.db})

	op := &acquireOperation{
		id: opID, workID: req.WorkID, generation: req.Generation,
		payload: payload, effects: effects,
	}
	if err := m.ops.Run(ctx, op); err != nil {
		cleanupErr := m.finishOrRollBack(ctx, opID)
		if cleanupErr != nil {
			return nil, fmt.Errorf("lease: acquire %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return nil, fmt.Errorf("lease: acquire %s: %w", opID, err)
	}
	return leases, nil
}

// ValidateAll re-reads every lease file and checks, cheaply and without any
// network access, that each one still matches its holder identity and that
// its recovery token matches the token the journal records for that path and
// operation. The first failing lease stops the pass.
func (m *Manager) ValidateAll(ctx context.Context, leases []Lease) error {
	// Load each distinct operation's payload once; the payload is the
	// recorded token store.
	tokensByOp := map[identity.OperationID]map[string]identity.Token{}
	for _, l := range leases {
		if _, ok := tokensByOp[l.OpID]; ok {
			continue
		}
		tokens, err := m.journaledTokens(ctx, l.OpID)
		if err != nil {
			return err
		}
		tokensByOp[l.OpID] = tokens
	}
	for _, l := range leases {
		content, err := readLeaseFile(l.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("lease: validate %s: %w", l.Path, ErrLeaseMissing)
			}
			return fmt.Errorf("lease: validate %s: %w", l.Path, err)
		}
		if content != l.Content {
			return fmt.Errorf("lease: validate %s: content differs from journaled content (workspace %s vs %s, work %s vs %s, generation %d vs %d): %w",
				l.Path, content.WorkspaceID, l.Content.WorkspaceID,
				content.WorkID, l.Content.WorkID, content.Generation, l.Content.Generation, ErrLeaseDrift)
		}
		want, ok := tokensByOp[l.OpID][l.Path]
		if !ok {
			return fmt.Errorf("lease: validate %s: journal of %s records no token for this path: %w",
				l.Path, l.OpID, ErrLeaseTokenMismatch)
		}
		if want != content.RecoveryToken {
			return fmt.Errorf("lease: validate %s: token does not match journal: %w", l.Path, ErrLeaseTokenMismatch)
		}
	}
	return nil
}

// journaledTokens decodes the acquisition payload of opID into a path→token
// map.
func (m *Manager) journaledTokens(ctx context.Context, opID identity.OperationID) (map[string]identity.Token, error) {
	rec, err := m.db.Operation(ctx, opID)
	if err != nil {
		return nil, fmt.Errorf("lease: validate: load journal of %s: %w", opID, err)
	}
	var p acquirePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return nil, fmt.Errorf("lease: validate: decode journal of %s: %w", opID, err)
	}
	tokens := make(map[string]identity.Token, len(p.Targets))
	for _, t := range p.Targets {
		tokens[t.Path] = t.Token
	}
	return tokens, nil
}

// ReleaseAll releases every lease: each removal is a journaled effect (so an
// interrupted release is rolled forward on recovery), the removal removes
// only the token-matching file, and a missing file is a no-op success. The
// work's active row is left untouched — deactivation is the workflow
// engines' archive/abandon step.
func (m *Manager) ReleaseAll(ctx context.Context, leases []Lease) error {
	if len(leases) == 0 {
		return nil
	}
	opID, err := identity.NewOperationID()
	if err != nil {
		return fmt.Errorf("lease: generate operation id: %w", err)
	}
	workID := leases[0].Content.WorkID
	var effects []operation.Effect
	for _, l := range leases {
		effects = append(effects, NewRemoveLeaseEffect(l.Path, l.Content))
	}
	op := &releaseOperation{id: opID, workID: workID, effects: effects}
	if err := m.ops.Run(ctx, op); err != nil {
		// A blocked removal (a foreign lease on one path) must not leave a
		// prepared release operation that every future recovery pass retries
		// in vain: roll the whole release back, which re-creates the
		// already-removed leases and leaves the foreign file alone.
		cleanupErr := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationPolicy(ctx, opID, string(operation.RollBack))
		})
		if cleanupErr == nil {
			cleanupErr = m.ops.RecoverOne(ctx, opID)
		}
		if cleanupErr != nil {
			return fmt.Errorf("lease: release %s: %v (cleanup: %v)", opID, err, cleanupErr)
		}
		return fmt.Errorf("lease: release %s: %w", opID, err)
	}
	return nil
}

// releaseOperation is the journaled idempotent release.
type releaseOperation struct {
	id      identity.OperationID
	workID  identity.WorkID
	effects []operation.Effect
}

func (o *releaseOperation) ID() identity.OperationID    { return o.id }
func (o *releaseOperation) Kind() string                { return OpKindRelease }
func (o *releaseOperation) WorkID() identity.WorkID     { return o.workID }
func (o *releaseOperation) Generation() int64           { return 0 }
func (o *releaseOperation) Policy() operation.Policy    { return operation.RollForward }
func (o *releaseOperation) Payload() any                { return map[string]int{"leases": len(o.effects)} }
func (o *releaseOperation) Effects() []operation.Effect { return o.effects }

// Recover is the operation-package recovery hook for lease effects: it
// registers every lease effect kind with the operation manager, decides for
// each pending lease acquisition whether recovery rolls forward or rolls the
// token-matching leases back, and drives the whole pending pass to terminal
// states.
//
// The decision, per pending acquisition: if the checkpoint commit marker
// exists, activation is never rolled back and recovery rolls forward
// (finishing the projection); otherwise, if every remaining lease is still
// acquirable — no foreign lease occupies a not-yet-applied target — recovery
// rolls forward; otherwise the operation is switched to roll-back and only
// the token-matching leases are removed.
func (m *Manager) Recover(ctx context.Context) error {
	m.registerEffects()
	pending, err := m.db.PendingOperations(ctx)
	if err != nil {
		return fmt.Errorf("lease: recover: list pending operations: %w", err)
	}
	for _, rec := range pending {
		if rec.Kind != OpKindAcquire || rec.State != store.OpPrepared {
			continue
		}
		policy, err := m.decidePolicy(ctx, rec)
		if err != nil {
			return err
		}
		if policy != operation.Policy(rec.Policy) {
			if err := m.db.Update(ctx, func(tx *store.Tx) error {
				return tx.SetOperationPolicy(ctx, rec.ID, string(policy))
			}); err != nil {
				return fmt.Errorf("lease: recover: switch %s to %s: %w", rec.ID, policy, err)
			}
		}
	}
	return m.ops.RecoverPending(ctx)
}

// finishOrRollBack drives one pending lease operation to a terminal state
// with the same decision recovery uses: the commit marker's presence wins
// (roll forward — activation is never rolled back), then acquirability.
func (m *Manager) finishOrRollBack(ctx context.Context, opID identity.OperationID) error {
	rec, err := m.db.Operation(ctx, opID)
	if err != nil {
		return fmt.Errorf("lease: cleanup: %w", err)
	}
	switch rec.State {
	case store.OpFinalized, store.OpRolledBack:
		return nil
	}
	policy, err := m.decidePolicy(ctx, rec)
	if err != nil {
		return err
	}
	if policy != operation.Policy(rec.Policy) {
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationPolicy(ctx, rec.ID, string(policy))
		}); err != nil {
			return fmt.Errorf("lease: cleanup: switch %s to %s: %w", rec.ID, policy, err)
		}
	}
	return m.ops.RecoverOne(ctx, opID)
}

// decidePolicy chooses the recovery policy for a prepared acquisition: roll
// forward unless the commit marker is absent and a remaining lease is held
// by someone else.
func (m *Manager) decidePolicy(ctx context.Context, rec store.OperationRecord) (operation.Policy, error) {
	if operation.Policy(rec.Policy) == operation.RollBack {
		return operation.RollBack, nil
	}
	var p acquirePayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return "", fmt.Errorf("lease: decode journal of %s: %w", rec.ID, err)
	}
	// The commit marker's presence — durable before its journal row —
	// forbids rolling the activation back. The marker must name THIS
	// operation: a sibling acquisition that lost the race to the winner's
	// marker must still roll its own token-matching leases back instead of
	// rolling forward onto leases it cannot hold.
	if data, err := readControlFile(SentinelPath(p.ControlRoot, p.WorkID)); err == nil {
		sentinel, serr := ReadSentinelBytes(data)
		if serr != nil {
			return "", fmt.Errorf("lease: read commit marker of %s: %w", rec.ID, serr)
		}
		if sentinel.OperationID == rec.ID {
			return operation.RollForward, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("lease: read commit marker of %s: %w", rec.ID, err)
	}
	rows, err := m.db.OperationEffects(ctx, rec.ID)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.Kind != kindCreateLease || row.State == store.EffectApplied {
			continue
		}
		var pl createLeasePayload
		if err := json.Unmarshal(row.Payload, &pl); err != nil {
			return "", fmt.Errorf("lease: decode effect %d of %s: %w", row.Seq, rec.ID, err)
		}
		existing, err := readLeaseFile(pl.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", err
		}
		if existing != pl.Content {
			return operation.RollBack, nil
		}
	}
	return operation.RollForward, nil
}

// registerEffects installs every lease effect prototype for recovery
// dispatch. Registration is idempotent.
func (m *Manager) registerEffects() {
	m.ops.RegisterEffect(&createLeaseEffect{})
	m.ops.RegisterEffect(&sentinelEffect{})
	m.ops.RegisterEffect(&activateEffect{db: m.db})
	m.ops.RegisterEffect(&removeLeaseEffect{})
}
