# Configure final skeptic review for to

- **Status:** Partially superseded by 0090 (scope of engines)
- **Date:** 2026-11-11

## Context

The to workflow requires at least one independent final skeptic, even for small
changes. Projects need an explicit speed and assurance trade-off without editing
bundled instructions.

## Decision

Use `[workflow].final_skeptic` for to, defaulting to one to preserve the
existing minimum. The other choices are off, two and risk-based auto. The
policy is instruction-enforced, not a binary proof of independent review.
Other engines retain their existing behavior.

## Consequences

Projects can reduce or increase review cost and record the choice. Disabling
review reduces assurance and never overrides explicit review requirements.
Verdicts apply only to the candidate reviewed and must be refreshed on change.
