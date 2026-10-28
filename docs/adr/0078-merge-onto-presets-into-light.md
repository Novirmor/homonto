# Merge onto fix and tweak into onto light

- **Status:** Accepted
- **Date:** 2026-10-28
- **Amends:** [0062](0062-ask-once-before-unselected-workflow-creation.md) (the offered paths)

## Context

onto shipped two presets, `fix` and `tweak`. The binary never told them apart:
every gate asked only "is this a preset?". The skills differed in two places,
the entry wording (bug versus non-bug) and the TDD default. Yet the user was
asked to choose between them, which is a classification the model can make
from repository evidence. Light changes could also run an optional skeptic, or
both skeptics when risk promoted them to full verification, so the boundary
with full onto was blurred.

## Decision

One preset, `onto light` (`--workflow light`, `/onto-light`, skill
`onto-light`), replaces both. Open-lite classifies the request: a bug gets
`tdd-mode tdd` with a failing reproduction test first; other testable changes
get `tdd`; content edits get `direct`.

**Light never runs a skeptic.** That is its defining difference from full onto.
The eligibility line stays: one module, at most five non-test files, no new
capability or changed requirement. A security-sensitive surface now also
excludes light. Work that measures `full`, or reaches a risk trigger, escalates
to the full workflow; it is never verified at full scale inside a light change.
The binary enforces this: `onto set verify-scale` and `onto scale --set` refuse
`full` for a light change and name the escalation command.

The user still chooses light or full at selection (ADR 0062). `fix` and `tweak`
are removed from the CLI, catalog and docs. State files that record them load
as `light`, so in-flight changes continue; the next write records `light`.

## Consequences

Selection offers one fewer onto path, and the skill set loses a near-duplicate
skill. Small risky changes cost more: they go through design and two skeptics,
not a light verify. Scripts calling `onto new --workflow fix|tweak` or
`/onto-fix` and `/onto-tweak` break and must move to `light`. In-flight
proposals that still say `Preset: fix` or `Preset: tweak` keep that text until
edited.
