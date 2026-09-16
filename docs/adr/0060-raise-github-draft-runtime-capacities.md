# Raise GitHub draft runtime capacities

- **Status:** Accepted
- **Date:** 2026-09-16

## Context

The GitHub draft plugin originally allowed 64 sessions, 128 retained drafts, and
512 correlated question request IDs per plugin instance. Custom skills and
long-running OpenCode servers can legitimately cross those ceilings without a
restart. The three records have different memory costs: sessions and request IDs
are small, while a draft retains item bodies and remote snapshots.

## Decision

We will raise the per-instance ceilings to 4,096 sessions, 1,024 drafts, and
65,536 request IDs. We reject applying one multiplier to every limit because
drafts are substantially heavier than identity records.

This change adds headroom without changing lifecycle or authorization semantics.
Reclaiming terminal records remains separate work and must preserve uncertain-send
reconciliation and replay protection.

## Consequences

Long-running custom-agent publication sessions are much less likely to require a
plugin restart because of capacity. A hostile or pathological client can retain
more memory before the fail-closed limits apply, especially through large drafts.
The draft ceiling therefore grows less aggressively than the lightweight limits.
