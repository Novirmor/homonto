# Make onto and to complementary

- **Status:** Accepted
- **Date:** 2026-09-04

Relaxes the exclusive-choice rule that
[`docs/adr/0028-to-promote-into-onto.md`](0028-to-promote-into-onto.md) left
standing ("a repository still runs exactly one framework at a time") and that
[`docs/adr/0040-configure-one-workflow-root.md`](0040-configure-one-workflow-root.md)
rests on.

## Context

Exclusivity assumed the framework choice is per-repository and static. In
practice a repository holds both kinds of work at once: gated changes that
need evidence, spec deltas, and a second reader, and simple changes where
that machinery costs more than it protects. Since v0.16 the workflow is
chosen by selecting its primary agent, the records live in disjoint
directories (`changes/` vs `tasks/`), and the agents, commands, and slash
entry points are namespaced — the only thing enforcing one-framework-per-repo
was a load-time validation, and `to promote` already crossed the boundary.

## Decision

The two frameworks are complementary. A repository may declare
`[frameworks.onto]` and `[frameworks.to]` together; both project side by
side, and the primary agent the user selects picks the workflow **per
change**. The invariants:

- **Global name uniqueness.** An active change name exists in exactly one
  workflow. `onto new` and `to new` refuse a name already active in the
  sibling tree and name the bridge command instead; both doctors report a
  duplicate as a finding.
- **One lineage per change.** Conversions transfer ownership; they never
  create an unrelated change. The active workspace carries a neutral control
  plane (`.workflow/lineage.json`, `events/*.json` receipts,
  `snapshots/<operation-id>/<workflow>/`) that both binaries read. Snapshots
  exclude `.workflow/` itself, so repeated conversions append history
  instead of nesting it; v0.18 `imported-to/` provenance is adopted into
  the store on the next conversion.
- **Reversible while unchanged.** Converting back with no edits to the
  generated target nor the snapshot restores the previous workspace
  byte-for-byte (digest-verified) and appends a restore event. After edits
  it is a fresh conversion that snapshots the edited bytes.
- **Receipt-verified completion.** A retry reports success only when the
  installed target holds a receipt naming the direction, source, and target
  with a matching snapshot. Directory existence alone never proves a
  conversion, and failed preconditions (unknown source, occupied target,
  terminal or mismatched state) leave nothing staged.
- **Phase-aware demotion.** `onto demote <name> --yes` moves open/design to
  phase `plan`; build/verify to phase `do` only when the task list
  translates into a doctor-clean `to` plan (checkboxes with
  `Files:`/`Change:`/`Verify:` and a `Final Verify:` line); closed,
  abandoned, and archived changes are refused. Promotion stays conservative
  at `full/open`.
- **Serialized writers.** Every onto mutator (advance, set, abandon, close,
  bypass, merge-deltas, complete-integration, demote) takes the onto
  workspace lock; the bridges take the `to` workspace lock, the shared
  destination lock, and (demote) the onto lock in one fixed global order.
  Coexistence does not authorize two writers on one change.

## Consequences

A repository runs both loops concurrently — simple work stays light, gated
work keeps its evidence — and a change that outgrows (or no longer needs)
its workflow converts without losing the record. The costs: a dual config
needs model blocks for both frameworks' agents and must declare them at the
same effective scope (shared resources like the `homonto` skill dedupe by
effective placement, not raw target lists); doctor's version-skew checks
apply per framework; and the skills guide per-change selection instead of a
one-time repository choice. ADR 0028's "never hold both" consequence no
longer holds; its promotion mechanics were replaced by the shared conversion
engine (preconditions-first, pre-minted identities for deterministic
resumes, authenticate-then-generate staging).