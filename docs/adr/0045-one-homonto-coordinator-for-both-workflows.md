# One homonto coordinator for both workflows

- **Status:** Accepted
- **Date:** 2026-09-06

## Context

ADR 0031 gave each workflow its own selectable primary agent (`onto`, `to`)
so the user could pick a workflow per change. In practice that split the
coordinator role in two: every cross-cutting addition — GitHub issue/PR
intake, workflow selection, shared delegation policy — had to be duplicated
across both primaries or pushed into a third agent that could not reach
either workflow. The planned `h` GitHub bundle needs exactly such an agent:
it must fetch from GitHub, ask "to or onto?", and then drive whichever
dispatcher the answer selects, while workers stay read-only.

## Decision

We will replace the two per-framework primaries with one shared `homonto`
primary agent. Both the onto and the to framework manifests declare it from
the same catalog path (`subagents/homonto.md`), so installing either — or
the `h` companion, which depends on both — installs the one coordinator.
Every workflow slash command (`/onto`, `/to`, and the later `/h-*`) routes
`agent: homonto`. The coordinator loads whichever dispatcher skill the
change's workflow requires; dispatcher skills remain the per-framework
doctrine and are unchanged in authority.

## Consequences

- **Breaking:** configs carrying a `[subagents.onto.opencode]` or
  `[subagents.to.opencode]` model block fail at load ("tunes an agent that
  is not installed") until the block is renamed to
  `[subagents.homonto.opencode]`. There is no silent migration; the error
  names the fix.
- Declared-repo `external_directory` access (ADR 0039) now renders for
  `homonto` plus both implementers, gated on any builtin workflow framework
  being installed (`onto`, `to`, or `h`).
- The shared agent is declared by two frameworks; identical-path declarations
  collapse in the catalog's shared index, and framework placements must agree
  (scope/targets) exactly as the shared `homonto` skill already requires.
- One prompt carries both workflows' orchestration policy; dispatcher skills
  stay the single source for phase mechanics, so the prompt stays a role
  description rather than a second doctrine.
