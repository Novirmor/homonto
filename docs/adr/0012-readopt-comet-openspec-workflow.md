# Re-adopt Comet + OpenSpec + Superpowers as the development workflow

- **Status:** Superseded by 0023
- **Date:** 2026-07-10
- **Change:** comet-workflow-migration

## Context

ADR 0005 (2026-07-04) adopted **onto** — eight self-contained markdown skills
with a single `docs/` artifact tree — and retired the external spec-lifecycle
CLI and guard/state scripts. The stated motivation was portability and a
self-describing workflow with no install step.

In practice, dogfooding onto surfaced gaps that the external stack already
solves: onto's agent-managed `state.yaml` has no hard phase gate (drift is only
self-healed on the next dispatch), its verification and lint discipline lives in
prose rather than an enforcing tool, and its WHAT/HOW artifacts are not backed by
a queryable change model. On 2026-07-09 the workflow was migrated back to
**Comet** coordinating **OpenSpec** (canonical WHAT) and **Superpowers**
(canonical HOW), with `.comet.yaml` phase state enforced by `comet-guard` and
`openspec` as the change registry. The migration shipped its specs and guides
(`docs/specs/comet-workflow.md`, `docs/guides/comet-workflow.md`, the onto specs
re-marked legacy) but never recorded the **decision reversal** — this ADR closes
that gap. It supersedes ADR 0005; it does not restore the machinery ADR 0005
removed for any *product* purpose — onto remains a shipped product framework in
`homonto/skills/`, only its role as *this repo's* development workflow ends.

## Decision

We will develop through **Comet** as the entry point (`/comet` and its
open/design/build/verify/archive presets), with **OpenSpec** canonical for WHAT
(changes under `openspec/changes/<name>/`, main specs under `openspec/specs/`
after archive) and **Superpowers** canonical for HOW (design docs, plans, and
reports under `docs/superpowers/`). Phase state lives in each change's
`.comet.yaml` and is gate-enforced by `comet-guard`; agents inspect
`openspec/changes/` and `.comet.yaml` before starting or resuming work.

`docs/changes/` becomes **legacy onto history** — readable for context, never
edited or used as active workflow state. Existing `docs/specs/*.md` remain as
transition documents until a separate conversion change migrates them into
OpenSpec. ADR 0011's numbering/staging convention still governs `docs/adr/`,
which stays the project's decision log across whichever workflow is active; this
ADR was authored directly (not staged in an onto change workspace) because it
ratifies an already-shipped decision.

## Consequences

- Hard phase gating and an enforcing lint/verify/archive pipeline return, at the
  cost of the external install (Comet + OpenSpec + Superpowers skills) that
  ADR 0005 had eliminated.
- Two workflow vocabularies now coexist in the tree: active development uses
  `openspec/` + `docs/superpowers/`; `docs/changes/archive/` and the onto specs
  are historical. Agents must not open new `docs/changes/*` onto workspaces for
  Homonto development.
- onto stays a first-class **product** framework in the catalog and in
  `homonto/skills/`; this reversal is scoped to the repo's own dev process.
- The in-flight `catalog-foundation-skills` OpenSpec change (design phase)
  continues unchanged under Comet.
