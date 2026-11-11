# Apply final skeptic policy to onto and no

- **Status:** Accepted
- **Date:** 2026-11-11

## Context

The original final-skeptic proposal also covered onto and no. The focused
implementation merged for to left both engines out, and onto light explicitly
prohibited skeptics.

## Decision

Apply the project-wide policy to onto, including light, and no. When not set,
onto and no default to two passes; to keeps its existing default of one. Add
the read-only no-skeptic and inherit its reviewer model and variant when not
configured separately.

## Consequences

No and onto light do more work by default. Explicit off reduces assurance, but
cannot override user or policy review requirements. This policy remains
instruction-enforced rather than proof that an independent review occurred.
