# Make the tooling preflight warn and proceed, never halt

- **Status:** Accepted; mechanism superseded by 0023
- **Date:** 2026-07-04
- **Change:** address-deep-review

> The mechanism this record governed — the onto dispatcher's rtk and
> Graphify preflight, and its notes.md grounding record — was removed by
> [0023](0023-rebuild-homonto-as-workflow-orchestrator.md). The principle
> survives unchanged and is still how this repository is developed:
> preflight warns and proceeds; no missing tool halts the workflow.

## Context

The onto dispatcher's preflight hard-required rtk and graphify: a missing
tool HALTed the workflow with install instructions and an explicit "do not
continue in a degraded mode" rule. The 2026-07-04 deep review found this
indefensible: halting an entire development workflow on a missing *token
optimizer* blocks work to protect a discount, and requiring an external
product (graphify) with an install URL in the halt text directly
contradicts onto's self-containment claim — the guide even denied the
degraded fallback the skill itself already contained. Alternatives
considered: keep the halts (rejected — a workflow that refuses to run is
worse than one that runs at higher cost), or drop the preflight entirely
(rejected — the tools are genuinely valuable and the user should hear when
they are missing).

## Decision

We will make the tooling preflight advisory: it warns and proceeds, never
halts. Missing rtk → warn that token costs will be higher and continue.
Missing graphify with no existing index → warn, record
`grounding: direct file reading (graphify unavailable)` in the change's
notes.md Grounding section, and continue. Staleness keeps its
ask-or-proceed treatment; indexing remains the user's decision. No
preflight outcome blocks the workflow.

## Consequences

- Degraded-but-working sessions: onto runs on any machine, at higher token
  cost without rtk and with weaker grounding without graphify — but it
  runs.
- Grounding fallbacks are recorded, not silent: notes.md and the
  proposal/design Grounding sections show when claims came from direct
  file reading instead of graph queries.
- This partially reverses ADR 0005's stance that "rtk and graphify are
  hard-required tooling" — the tools stay recommended and preflight-checked,
  but the hard requirement is withdrawn.
- The self-containment claim becomes true: nothing external is needed for
  the workflow to function.
