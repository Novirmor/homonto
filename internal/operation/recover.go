package operation

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// RecoverOne drives one journaled operation to a terminal state, leaving
// every other pending operation untouched. In-process failure cleanup uses it
// so that a failed run never recovers (and never mis-decides policy for) a
// sibling operation another goroutine is still executing.
func (m *Manager) RecoverOne(ctx context.Context, id identity.OperationID) error {
	rec, err := m.db.Operation(ctx, id)
	if err != nil {
		return fmt.Errorf("operation: load %s for recovery: %w", id, err)
	}
	switch rec.State {
	case store.OpFinalized, store.OpRolledBack:
		return nil
	}
	return m.recoverOne(ctx, rec)
}

// RecoverPending drives every journaled-but-unfinished operation to a
// terminal state, oldest first, per each operation's persisted policy:
//
//   - pending:  Prepare never committed, so no side effect ever ran; the
//     operation is closed as rolled_back under both policies.
//   - prepared, roll_forward: effects not yet recorded as applied are
//     re-applied (idempotently, from their persisted payloads) and the
//     operation is finalized. A row recorded as failed switches the
//     operation to roll-back instead: rolling forward past a failed Apply
//     is impossible.
//   - prepared, roll_back: effects recorded as applied are reverted in
//     reverse apply order and the operation is closed as rolled_back. A
//     failed row is closed as-is — never re-applied and never reverted.
//
// Recovery is idempotent: every transition it makes is journaled the same
// way Run's are, so an interrupted recovery is recovered the same way.
// The first failure stops the pass and returns; operations after it in the
// pending list are left untouched for the next pass.
func (m *Manager) RecoverPending(ctx context.Context) error {
	pending, err := m.db.PendingOperations(ctx)
	if err != nil {
		return fmt.Errorf("operation: list pending operations: %w", err)
	}
	for _, rec := range pending {
		if err := m.recoverOne(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

// recoverOne drives one pending operation to a terminal state.
func (m *Manager) recoverOne(ctx context.Context, rec store.OperationRecord) error {
	switch rec.State {
	case store.OpPending:
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationState(ctx, rec.ID, store.OpRolledBack)
		}); err != nil {
			return fmt.Errorf("operation: abort pending %s: %w", rec.ID, err)
		}
		failpoint("rolled-back")
		return nil
	case store.OpPrepared:
		rows, err := m.db.OperationEffects(ctx, rec.ID)
		if err != nil {
			return fmt.Errorf("operation: load effects of %s: %w", rec.ID, err)
		}
		if Policy(rec.Policy) == RollBack {
			return m.rollBack(ctx, rec.ID, rows)
		}
		return m.rollForward(ctx, rec.ID, rows)
	default:
		return fmt.Errorf("operation: %s: unexpected pending state %q", rec.ID, rec.State)
	}
}

// rollForward re-applies the effects of a prepared operation that are not
// yet recorded as applied — from their persisted payloads, so identities
// (tokens) match the original run — and finalizes the operation. Already
// applied effects are skipped, which is what makes interrupted recovery
// resumable. A failed row makes rolling forward impossible (its Apply
// errored and must never re-run), so the operation is switched to
// roll-back — the switch is its own committed step, so an interrupted pass
// re-decides — and closed there.
func (m *Manager) rollForward(ctx context.Context, id identity.OperationID, rows []store.EffectRow) error {
	for _, row := range rows {
		if row.State != store.EffectFailed {
			continue
		}
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetOperationPolicy(ctx, id, string(RollBack))
		}); err != nil {
			return fmt.Errorf("operation: switch %s to roll-back after failed effect %d: %w", id, row.Seq, err)
		}
		return m.rollBack(ctx, id, rows)
	}
	for _, row := range rows {
		if row.State == store.EffectApplied {
			continue
		}
		eff, err := m.effectForRow(id, row)
		if err != nil {
			return err
		}
		if err := eff.Apply(ctx, EffectRecord{
			OpID: id, Seq: row.Seq, Kind: row.Kind, State: row.State, Payload: row.Payload,
		}); err != nil {
			return fmt.Errorf("operation: re-apply effect %d of %s: %w", row.Seq, id, err)
		}
		// Same unrecorded window as Run: performed but not yet journaled.
		failpoint(fmt.Sprintf("effect-applied-unrecorded:%s:%d", id, row.Seq))
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetEffectState(ctx, id, row.Seq, store.EffectApplied)
		}); err != nil {
			return fmt.Errorf("operation: journal effect %d of %s as applied: %w", row.Seq, id, err)
		}
		failpoint("effect-applied")
	}
	if err := m.db.Update(ctx, func(tx *store.Tx) error {
		return tx.SetOperationState(ctx, id, store.OpFinalized)
	}); err != nil {
		return fmt.Errorf("operation: finalize %s after roll-forward: %w", id, err)
	}
	failpoint("finalized")
	return nil
}

// rollBack reverts the recorded-applied effects of a prepared operation in
// reverse apply order and closes it as rolled_back. Rows never recorded as
// applied are closed without a Revert call (see RollBack). A failed row is
// left untouched: a failed Apply is terminal — never re-applied, never
// reverted — and the failed marker is the row's lasting state.
func (m *Manager) rollBack(ctx context.Context, id identity.OperationID, rows []store.EffectRow) error {
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if row.State == store.EffectFailed {
			continue
		}
		if row.State == store.EffectApplied {
			eff, err := m.effectForRow(id, row)
			if err != nil {
				return err
			}
			if err := eff.Revert(ctx, EffectRecord{
				OpID: id, Seq: row.Seq, Kind: row.Kind, State: row.State, Payload: row.Payload,
			}); err != nil {
				return fmt.Errorf("operation: revert effect %d of %s: %w", row.Seq, id, err)
			}
			// Mirror of Run's unrecorded window: reverted but not yet journaled.
			failpoint(fmt.Sprintf("effect-reverted-unrecorded:%s:%d", id, row.Seq))
		}
		if err := m.db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetEffectState(ctx, id, row.Seq, store.EffectReverted)
		}); err != nil {
			return fmt.Errorf("operation: journal effect %d of %s as reverted: %w", row.Seq, id, err)
		}
		failpoint("effect-reverted")
	}
	if err := m.db.Update(ctx, func(tx *store.Tx) error {
		return tx.SetOperationState(ctx, id, store.OpRolledBack)
	}); err != nil {
		return fmt.Errorf("operation: roll back %s: %w", id, err)
	}
	failpoint("rolled-back")
	return nil
}

// effectForRow resolves the registered prototype for a journal row's kind.
func (m *Manager) effectForRow(id identity.OperationID, row store.EffectRow) (Effect, error) {
	eff, ok := m.effectFor(row.Kind)
	if !ok {
		return nil, fmt.Errorf("operation: %s effect %d: no effect registered for kind %q", id, row.Seq, row.Kind)
	}
	return eff, nil
}
