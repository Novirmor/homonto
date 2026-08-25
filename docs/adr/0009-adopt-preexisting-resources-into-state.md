# Adopt pre-existing matching resources into state via a silent apply-time action

- **Status:** Superseded by 0023
- **Date:** 2026-07-05
- **Change:** state-source-of-truth

## Context

When a declared, non-secret key already matches on disk, both adapters emit
`noop` and never call `st.Set`, so the key never enters `state.json`. Imported
or manually pre-existing resources therefore look managed but are invisible to
pruning (the prune loop iterates only `st.Keys(tool)`) and to state-gated drift
checks.

The obvious fix — "record state during the `noop` path" — collides with the
apply flow: `homonto apply` short-circuits when `plan.HasChanges` is false
(every action is `noop`). A plain-`noop` adoption would therefore never run in
its primary scenario: a config whose declared keys already all match disk, with
nothing else to apply. Adoption must count as apply-time *work* without
appearing as a tool-file change (the user chose silent adoption, and adoption
writes only `state.json`).

Alternatives considered: overloading `noop` (muddies the "No changes" path,
which would then secretly write state, and requires apply to always reconcile);
adopting eagerly only as a side-effect of other applies (breaks the
primary scenario, since a key must be adopted while still declared+matching).

## Decision

We will add a first-class `adopt` action to `adapter.Change`. Plan emits
`adopt` (instead of `noop`) for a declared, non-secret key that is present on
disk and equals desired but whose recorded `Entry.Applied` hash is absent
(never applied / imported / pre-existing) or stale (the on-disk value was
reconciled out of band to a value that now equals desired). A true `noop`
requires `inState && Entry.Applied == secret.Hash(canonical(disk))` — the same
condition the secret branch already uses, making the two branches symmetric. `adopt` renders no plan diff
line. On apply the adapter records the key in state
(`Applied = hash(canonical(resolve(desired)))`, equal to `hash(canonical(disk))`
by construction) **without writing any tool file**. `apply` runs this
reconciliation even when adoption is the only pending work — state-only, with
no `[y/N]` prompt — reporting `Reconciled N pre-existing resource(s) into
state.` A new `plan.HasAdoptions` distinguishes this from `HasChanges`. Secret
keys are never adopted; an unrecorded secret key re-applies as `update`, as
today.

## Consequences

- Imported/pre-existing declared resources become visible to pruning and drift
  after the next apply — closing NEXT_AGENT gap #1.
- A recorded key whose disk was reconciled out of band to the desired value
  gets its stale `Applied` refreshed on apply, so disk-vs-state drift for it
  clears instead of repeating forever (the alternative — a `noop` that never
  refreshes `Applied` — leaves permanent, non-actionable phantom drift).
- The `[y/N]` prompt and the terraform diff remain reserved for tool-file
  changes; adoption never prompts and never renders, matching its "converge
  quietly" intent.
- A fourth action string threads through the four action-literal sites
  (Plan, Render, HasChanges, Apply) plus the apply.go flow — a contained,
  well-tested surface.
- Refines the pipeline established in ADR 0001 (adds a state-only reconcile
  path); does not supersede it.
