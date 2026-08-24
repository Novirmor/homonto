package operation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// Operation is one journaled state-machine run. The interface carries
// everything the journal persists; the effects carry the behavior.
type Operation interface {
	// ID uniquely names the run; re-running the same ID fails.
	ID() identity.OperationID
	// Kind names the operation for factory dispatch and debugging.
	Kind() string
	// WorkID scopes the operation to a work unit; empty means
	// workspace-level.
	WorkID() identity.WorkID
	// Generation is the optimistic-concurrency generation the operation
	// runs at.
	Generation() int64
	// Policy selects roll-forward or roll-back on crash recovery.
	Policy() Policy
	// Payload returns the operation's JSON-marshalable parameters.
	Payload() any
	// Effects lists the operation's side effects in apply order (seq).
	Effects() []Effect
}

// Manager runs operations under the journal protocol and recovers pending
// ones after a crash. It is safe for concurrent use.
type Manager struct {
	db *store.DB

	mu      sync.RWMutex
	effects map[string]Effect
}

// NewManager returns a Manager journaling through db.
func NewManager(db *store.DB) *Manager {
	return &Manager{db: db, effects: map[string]Effect{}}
}

// RegisterEffect registers the prototype dispatched for an effect kind
// during recovery. Registration should happen before any Run whose effects
// of that kind must survive a crash; an unregistered kind fails recovery
// loudly and leaves the journal untouched.
func (m *Manager) RegisterEffect(proto Effect) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.effects[proto.Kind()] = proto
}

// effectFor returns the registered prototype for kind.
func (m *Manager) effectFor(kind string) (Effect, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	proto, ok := m.effects[kind]
	return proto, ok
}

// Run executes op under the journal protocol. Each state transition is its
// own committed transaction — that committed sequence is the crash journal —
// and the operation's side effects run between the commits:
//
//  1. intent:     the operation row is committed as pending.
//  2. prepare:    every effect's Prepare runs; the effect rows (kind,
//     payload, pending) and the prepared state commit together.
//  3. apply:      per effect in seq order, Apply performs the side effect
//     and then its applied row commits. An Apply error is terminal for
//     that effect: the row commits as failed and the policy commits as
//     roll_back before Run returns, so recovery rolls the operation back
//     instead of re-applying a failed effect.
//  4. finalize:   the finalized state commits.
//
// A crash anywhere after step 1 leaves a pending operation in the journal
// that RecoverPending drives to a terminal state per the operation's
// policy.
func (m *Manager) Run(ctx context.Context, op Operation) error {
	payload, err := json.Marshal(op.Payload())
	if err != nil {
		return fmt.Errorf("operation: marshal payload of %s: %w", op.ID(), err)
	}
	now := time.Now()

	if err := m.db.Update(ctx, func(tx *store.Tx) error {
		return tx.InsertOperation(ctx, store.OperationRecord{
			ID:         op.ID(),
			Kind:       op.Kind(),
			State:      store.OpPending,
			WorkID:     op.WorkID(),
			Generation: op.Generation(),
			Policy:     string(op.Policy()),
			Payload:    payload,
			CreatedAt:  now,
			UpdatedAt:  now,
		})
	}); err != nil {
		return fmt.Errorf("operation: journal %s as pending: %w", op.ID(), err)
	}
	failpoint("pending")

	type journalled struct {
		kind    string
		payload json.RawMessage
	}
	effects := op.Effects()
	prepared := make([]journalled, 0, len(effects))
	for i, eff := range effects {
		v, err := eff.Prepare(ctx)
		if err != nil {
			return fmt.Errorf("operation: prepare effect %d of %s: %w", i+1, op.ID(), err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("operation: marshal effect %d payload of %s: %w", i+1, op.ID(), err)
		}
		prepared = append(prepared, journalled{kind: eff.Kind(), payload: b})
	}
	if err := m.db.Update(ctx, func(tx *store.Tx) error {
		for i, j := range prepared {
			if err := tx.InsertEffect(ctx, store.EffectRow{
				OpID:    op.ID(),
				Seq:     int64(i + 1),
				Kind:    j.kind,
				State:   store.EffectPending,
				Payload: j.payload,
			}); err != nil {
				return err
			}
		}
		return tx.SetOperationState(ctx, op.ID(), store.OpPrepared)
	}); err != nil {
		return fmt.Errorf("operation: prepare %s: %w", op.ID(), err)
	}
	failpoint("prepared")

	for i, eff := range effects {
		rec := EffectRecord{
			OpID:    op.ID(),
			Seq:     int64(i + 1),
			Kind:    prepared[i].kind,
			State:   store.EffectPending,
			Payload: prepared[i].payload,
		}
		if err := eff.Apply(ctx, rec); err != nil {
			// A failed Apply is terminal for this effect: journal the row
			// failed, then leave the operation prepared under roll-back so
			// recovery reverts the earlier applied effects and never
			// re-applies this one. A crash inside this error path is the
			// failed-row mirror of the unrecorded apply window: before the
			// row commits the effect stays pending under roll-forward, and
			// the effect's own Apply idempotency must make that re-apply
			// safe.
			failpoint(fmt.Sprintf("effect-failed-unrecorded:%s:%d", op.ID(), rec.Seq))
			if ferr := m.db.Update(ctx, func(tx *store.Tx) error {
				return tx.SetEffectState(ctx, op.ID(), rec.Seq, store.EffectFailed)
			}); ferr != nil {
				return fmt.Errorf("operation: journal effect %d of %s as failed: %v (apply: %w)", i+1, op.ID(), ferr, err)
			}
			failpoint("effect-failed")
			if perr := m.db.Update(ctx, func(tx *store.Tx) error {
				return tx.SetOperationPolicy(ctx, op.ID(), string(RollBack))
			}); perr != nil {
				return fmt.Errorf("operation: switch %s to roll-back after failed effect %d: %v (apply: %w)", op.ID(), rec.Seq, perr, err)
			}
			return fmt.Errorf("operation: apply effect %d of %s: %w", i+1, op.ID(), err)
		}
		// The unrecorded window: the side effect is performed but its
		// applied row is not yet committed. A crash here means recovery
		// cannot know the effect ran — roll-forward re-applies (idempotency
		// contract), roll-back leaks it (documented RollBack semantics).
		failpoint(fmt.Sprintf("effect-applied-unrecorded:%s:%d", op.ID(), rec.Seq))
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetEffectState(ctx, op.ID(), rec.Seq, store.EffectApplied)
		}); err != nil {
			return fmt.Errorf("operation: journal effect %d of %s as applied: %w", i+1, op.ID(), err)
		}
		failpoint("effect-applied")
	}

	if err := m.db.Update(ctx, func(tx *store.Tx) error {
		return tx.SetOperationState(ctx, op.ID(), store.OpFinalized)
	}); err != nil {
		return fmt.Errorf("operation: finalize %s: %w", op.ID(), err)
	}
	failpoint("finalized")
	return nil
}
