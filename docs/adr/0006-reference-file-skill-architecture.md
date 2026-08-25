# Use bundled reference files for skill payloads (progressive disclosure)

- **Status:** Superseded by 0023
- **Date:** 2026-07-04
- **Change:** polish-onto-framework

## Context

onto v1 embedded everything in eight SKILL.md files. Adding canonical
artifact templates and detailed protocols (subagent build, adversarial
verify, close lint) inline would roughly double every skill's size and
load the full weight on every invocation, even when the payload is not
needed. Alternatives considered: everything-inline (A) and a homonto lint
subcommand (C, rejected — reintroduces the binary dependency ADR 0005
removed).

## Decision

We will keep SKILL.md files as lean process prose and ship templates and
detailed protocols as `references/` files inside each skill directory —
the skill instructs when to read which reference. References travel with
the same symlinks `homonto apply` creates, so they are available in every
repo without copying. Templates are canonical: structural deviation is a
close-phase lint finding.

## Consequences

- Context cost stays flat per dispatch; payload loads only when a phase
  needs it.
- One more failure mode — a missing references/ dir — handled by a
  documented degrade-don't-halt fallback.
- Contracts in docs/ point at templates instead of duplicating them; the
  phase-derivation table remains the single deliberate duplication.
