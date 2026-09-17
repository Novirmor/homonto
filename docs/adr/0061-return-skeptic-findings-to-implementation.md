# Return skeptic findings to implementation

- **Status:** Accepted
- **Date:** 2026-09-17

## Context

Fresh-context skeptics can refute an implementation after every planned task is
checked. Treating that result as an endpoint leaves a known defect waiting for a
new user prompt, despite an available implementer and an in-scope repair.

## Decision

We will turn every triaged, real, in-scope skeptic finding into a fully specified
unchecked repair task and continue through the owning workflow's implementer
loop. The repaired candidate receives fresh verification and skeptic evidence.
Only a scope or product decision, a justified declined finding, or a hard blocker
may end that loop.

We reject treating a recorded verification failure as a routine stopping point:
that preserves the finding but leaves a repairable defect unowned.

## Consequences

Verification can add implementation work and consume another full evidence round,
but a skeptic finding no longer strands a repairable change. The task record keeps
the repair auditable and preserves one serial writer per source scope.
