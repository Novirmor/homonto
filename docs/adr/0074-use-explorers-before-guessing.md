# Use explorers before guessing

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Explorer agents exist in both engines but are easy to skip: planning emphasizes
large cross-file questions, build mentions optional investigation, and debugging
can wait for repeated failures before fresh exploration.

## Decision

Default to the selected engine's explorer for meaningful discovery: unfamiliar
code or ownership before planning, unclear dependencies or impact before
implementation, and disputed finding origin or repair scope during triage.
Dispatch independent bounded questions concurrently, validate cited answers, and
reuse still-valid grounding. Keep trivial grounded work direct and preserve
dispatch fallbacks, read-only permissions and coordinator-owned decisions.

Reject both coordinator-only discovery and a mandatory explorer pass per task.
Exploration does not replace reviewer or skeptic evidence or authorize more work.

## Consequences

Discovery consumes more agent calls but should reduce speculative implementation
and unsupported repair demands. Bounded questions and evidence reuse limit cost;
shipped-prompt tests cannot prove dispatch frequency or answer quality in practice.
