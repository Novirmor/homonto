# Deliver continuation policy through model context

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

The coordinator and workflow skills already require lifecycle completion, but
agents can end with a progress report while an authorized next step remains.
OpenCode's build and custom agents receive observed workflow state without
necessarily loading the shared autonomy policy. Coordinator-only prompt changes
do not cover that path.

## Decision

We will deliver one static continuation reminder through both OpenCode plugin
versions' existing model-context and compaction hooks, separate from untrusted
workflow data. Before ending, agents check their current authorized assignment
for executable remaining work. Empty snapshots do not suppress this reminder;
they neither establish completion nor authorize workflow creation or selection.
Workers return at their assignment boundary; explicit endpoints, pauses,
authorization requirements, denials, and concrete blockers remain valid stops.

We retain ADR 0050's nonblocking observer. Idle-triggered synthetic prompts are
not a substitute: unfinished records cannot distinguish premature completion
from a deliberate pause, cancellation, or missing authorization.

## Consequences

The reminder reaches build, homonto, and custom agents when the integration is
enabled, with a small per-request context cost within the existing byte budget.
Disabled integrations rely on loaded skill/agent instructions. Runtime tests
prove delivery and boundaries, not model compliance; live task evaluation is
still needed to measure fewer premature stops.
