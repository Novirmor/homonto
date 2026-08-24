// Package operation implements the transactional effect journal: operations
// whose side effects run between individually-committed state transitions,
// so a crash leaves a persisted journal that recovery can roll forward or
// roll back per the operation's recorded policy.
package operation

import (
	"context"
	"encoding/json"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Policy is the crash-recovery policy persisted with each operation.
type Policy string

const (
	// RollForward completes a prepared operation after a crash by applying
	// the effects not yet recorded as applied. Apply must therefore be
	// idempotent: a crash between performing an effect and committing its
	// journal row leaves recovery to perform it again.
	RollForward Policy = "roll_forward"
	// RollBack closes a prepared operation after a crash by reverting the
	// effects recorded as applied, in reverse apply order. Effects never
	// recorded as applied are closed without a Revert call — whether their
	// side effect happened in the unrecorded window is unknowable, which is
	// why RollForward is the trusting default and RollBack the conservative
	// choice.
	RollBack Policy = "roll_back"
)

// EffectRecord is one journaled effect as persisted in the store. Payload is
// exactly the JSON produced by Prepare before the effect first ran, so
// recovery replays the same identity (tokens included) that the original
// run carried.
type EffectRecord struct {
	OpID    identity.OperationID
	Seq     int64
	Kind    string
	State   string
	Payload json.RawMessage
}

// Effect is one journaled side effect of an operation. Implementations must
// be stateless with respect to their identity: everything Apply and Revert
// need to act must come from the EffectRecord, because recovery dispatches
// on the persisted kind through a registered prototype, not on the original
// in-memory instance.
type Effect interface {
	// Kind names the effect for dispatch. Run persists it with each journal
	// row; recovery looks the implementation up by it
	// (Manager.RegisterEffect).
	Kind() string
	// Prepare computes — without performing — the effect and returns its
	// persisted payload, which must JSON-marshal (for example an
	// assignment token minted before any durable change).
	Prepare(ctx context.Context) (payload any, err error)
	// Apply performs the side effect described by rec. It MUST be
	// idempotent (see RollForward).
	Apply(ctx context.Context, rec EffectRecord) error
	// Revert undoes the side effect described by rec. It must tolerate
	// being called only for effects recorded as applied.
	Revert(ctx context.Context, rec EffectRecord) error
}

// failNow is the fault-injection seam for tests: when non-nil it is called
// at every journal boundary, immediately after the named state transition
// has been committed. A panic from the hook simulates process death with
// the committed journal intact. It is nil (a no-op) in production; tests
// own its entire lifecycle.
//
// Boundary points:
//
//   - "pending":         the operation's intent row is committed
//   - "prepared":        effect rows + prepared state are committed
//   - "effect-applied":  one effect's applied row is committed (per effect,
//     in seq order)
//   - "finalized":       the finalized state is committed
//   - "effect-reverted": one effect's reverted row is committed during
//     roll-back recovery (reverse seq order, including
//     rows closed without a Revert call)
//   - "rolled-back":     the rolled_back state is committed
//   - "effect-applied-unrecorded:<op>:<seq>": an effect's Apply returned but
//     its applied row is not yet committed (Run and roll-forward recovery) —
//     the window whose crash semantics are the idempotency contract
//     (RollForward re-applies) and the documented leak (RollBack cannot
//     revert what it never saw recorded)
var failNow func(point string)

// failpoint invokes the fault-injection hook, if installed.
func failpoint(point string) {
	if failNow != nil {
		failNow(point)
	}
}
