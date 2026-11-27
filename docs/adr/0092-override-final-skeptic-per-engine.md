# Override final skeptic policy per engine

- **Status:** Accepted
- **Date:** 2026-11-27

## Context

The shared final skeptic policy cannot express different assurance and cost
choices for no, to, and onto in the same project.

## Decision

Allow optional per-engine overrides. Resolve an override before the existing
shared setting and then the existing engine default. Retain the shared setting
instead of replacing it, so existing configurations keep their behavior.

## Consequences

Projects can choose independently, but must understand precedence and may
deliberately reduce assurance for an engine. Invalid overrides fail validation;
the policy remains instruction-enforced and cannot override explicit review
requirements.
