# Close journal crash windows with idempotent effects

- **Status:** Accepted
- **Date:** 2026-08-24

## Context

The runtime journal (`internal/operation`) drives side effects that live
outside the database, so bookkeeping and effect can never share one
transaction. Instead, every state transition — pending, prepared, each
effect's applied/reverted row, finalized, rolled_back — is its own commit
(commit-per-transition). A crash therefore leaves a journal that is
internally consistent but arbitrarily behind the effects it describes,
and two windows are unavoidable: after Apply performs but before its
applied row commits, and after Revert undoes but before its reverted row
commits. The rejected alternative — folding effects into the store
transaction or building exactly-once machinery around them — cannot work
for external side effects and was not attempted.

## Decision

We will close both unrecorded windows with an idempotency contract:

- Effect **Apply** MUST be idempotent: roll-forward recovery re-applies
  any effect whose row does not read applied.
- Effect **Revert** MUST be idempotent: roll-back recovery re-reverts
  any row still reading applied, so a crash in the revert window also
  converges to rolled_back.
- A **failed Apply is terminal for its effect**: Run journals the row as
  `failed` (a fourth effect state) and switches the operation to
  roll_back before returning. Recovery never re-applies a failed row and
  never reverts it — the failed marker is the row's lasting state. A
  prepared roll_forward operation found holding a failed row is switched
  to roll_back by recovery itself; that switch is its own committed
  step, so an interrupted pass re-decides.
- **RollForward is the default** because it is the trusting policy:
  re-applying from the journaled payload replays the same identity.
- **RollBack leaks by design**: an effect performed in the unrecorded
  apply window is never recorded, so roll-back closes its row without a
  Revert — that side effect survives the operation, unnameable by the
  journal.
- A pending operation never ran a side effect, so recovery aborts it to
  rolled_back under both policies.
- A read-only open of a database whose WAL still holds committed frames
  fails with `ErrUnrecoveredWAL`; the remedy is one read-write open (a
  recovery pass), not a weaker open mode.

## Consequences

Every effect implementation now carries the burden of idempotent Apply
and Revert — an effect that cannot be made idempotent cannot be
journaled. Delivery is at-least-once, never exactly-once: a re-apply or
re-revert after an unrecorded-window crash is normal behavior, and both
calls carry the token minted by the single Prepare. A RollBack operation
that crashed in the apply window leaves an orphaned side effect nothing
in the system can revert; operators get a correct journal, not a
restored world. The abort rule means an interrupted Run before Prepare
leaves no trace among effects. And the ErrUnrecoveredWAL remedy means a
read-only consumer of a crashed database cannot be served until some
writer runs once — accepted so read-only opens stay honest about what
they guarantee.

The failed-effect rule adds one more honest window: between a failed
Apply's return and its failed row's commit, the row still reads pending
under roll_forward, so recovery re-applies it under the ordinary
idempotency contract above. Closing that window is the effect's own
Apply idempotency — the gitx cherry-pick recognizes its own conflicted
stop (a CHERRY_PICK_HEAD naming the journaled commit) as already
applied, so a crashed conflicted pick is never re-run: neither the
"cherry-pick is already in progress" boot failure nor the duplicate
commit after an out-of-band continuation is reachable.
