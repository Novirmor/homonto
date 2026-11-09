---
name: expert-architecture
description: Design and review software architecture, service boundaries, data flows, reliability trade-offs, migration paths and architecture decisions. Use for system design or architectural changes, including preparing an implementation handoff.
---

# Architecture expertise

Use the [expert coordination contract](../expert/SKILL.md) for scope, handoffs
and engine composition.

## Ground the design

Read the existing system, project notes and accepted decisions before proposing
a replacement. Establish users and workloads, latency/availability needs, data
ownership, deployment constraints, team constraints and the requested horizon.
Separate measured facts, stated requirements, assumptions and open questions.
Ask about a missing constraint when it changes the preferred approach.

Map the current responsibilities, interfaces, data flows and trust boundaries.
Trace at least the important success path and failure path through actual
components. Cite file paths or supplied sources for claims about the current
system. Do not invent scale requirements to justify a distributed architecture.

## Develop the proposal

Compare the simplest viable approach with meaningful alternatives against the
actual constraints. Explain why the recommended boundary exists: ownership,
consistency, deployment, independent scaling or failure isolation. Include the
costs of retries, queues, caches and additional services where proposed.

Address relevant failure modes: duplicate or out-of-order messages, partial
failure, stale reads, contention, dependency outages, schema evolution and
recovery. Name consistency guarantees and their limits. Describe operability,
observability and a practical path from the current system to the proposal.

Use [the design outline](references/design.md) for a substantial deliverable;
omit irrelevant sections for a focused question. Mermaid source is useful for
context, sequence, deployment and entity diagrams. Keep labels consistent with
the prose. Structural checks do not prove a diagram renders correctly; inspect
it with an available renderer before claiming visual validation.

## Implementation and review

Give implementers concrete interfaces, invariants, migration steps and acceptance
criteria. Review actual code against these rather than preferring a fashionable
pattern. Challenge unsupported assumptions, hidden coupling, infeasible failure
recovery and alternatives dismissed without a reason.

Return proposed ADR content to the parent. Preserve accepted decisions; explain
which decision a new proposal would supersede rather than silently rewriting it.
Expert advice alone is not acceptance, implementation or independent review.
