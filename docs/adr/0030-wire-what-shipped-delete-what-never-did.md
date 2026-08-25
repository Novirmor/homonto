# Wire what shipped, delete what never did

- **Status:** Accepted
- **Date:** 2026-08-25

## Context

A post-cutover audit of the workflow-only tree (deadcode analysis plus a
documentation pass) found three kinds of gap left by the rewrite:

1. Two shipped paths skipped machinery the design says every work gets.
   Confirming a Change's classification created the work without leasing
   the members or writing the first checkpoint — `App.ConfirmChangePath`
   existed to do exactly that but no command reached it; the CLI's
   `decide` confirmed through the engine directly. And the workspace-open
   recovery pass ran before the lease and handoff effect kinds were
   registered with the operation manager, so a crash mid-lease-acquire or
   mid-handoff left a journal row no later command could recover — the
   workspace refused to open at all.

2. Whole subsystems were scaffolded but unreachable: the workspace rescan
   (explicit WS3 leftovers, commented as unfinished), the walk-up
   workspace locator, and a second verification-freshness model the
   engines never consulted — they reconcile against their own baselines.
   The self-update fetch/stage/activate path is implemented and tested
   but no CLI command invokes it, and release builds embed no signing
   root.

3. Documentation described intent as fact: an install command that
   currently builds the deleted product, a release checklist reusing an
   existing tag, a verification guide describing the deleted Docker gate,
   and config fields nothing reads presented as behavior.

## Decision

**Wire the two skipped paths, with regression tests.** Classification
confirmation activates the work (leases + first checkpoint) inside
`App.Decide` — the one decision that creates work is the one place
activation belongs — and workspace open registers every shipped effect
kind (lease via the manager's constructor, handoff via an exported
`RegisterEffects`) before the single recovery pass.

**Delete the never-reachable scaffolding rather than finishing it under
cover of a cleanup.** Rescan, the walk-up locator, and the parallel
freshness model are gone. Bringing any of them back is a deliberate
product decision with its own change, not a file waiting in the tree.

**Keep the unwired update path and say so.** `internal/update`'s
fetch/stage/activate is real, recovery of an interrupted activation is
wired into workspace open, and the docs now state plainly that no
shipped command fetches an update and no release build yet carries a
signing root. Exposing it is release-blocking work, deliberately not
done here.

**Docs tell the truth about the current tree** — install-from-source,
`<new-version>` above the highest legacy tag, the real gate steps — and
the redesign doc carries a historical-design banner.

## Consequences

A stale clone can still mutate its local state after another machine
takes over: `lease.ValidateAll` and `handoff.CheckpointGeneration` exist
and are tested, but consulting them before mutations (the stale-clone
ownership gate) is pending. That is a new refusal — product behavior
needing its own tests and record — not a bug fix to slip in here. The
handoff package doc now says exactly this instead of claiming the
engines already consult them.

The first workflow-only release cannot ship until the update path is
either exposed with a compiled trust root or the release claims are
rewritten again; `docs/release-checklist.md` and `docs/guides/updates.md`
carry that constraint.
