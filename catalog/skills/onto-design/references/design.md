# design.md — canonical template

The confirmed technical design. MUST NOT exist before the approach gate is
answered; once written, its `Status: Confirmed` line is what the
phase-derivation table keys on.

## Template

```markdown
# Design: <change-name>

Status: Confirmed
Confirmed: YYYY-MM-DD (<which approach, in a few words>)

## Summary

<the chosen approach in one paragraph; rejected alternatives in one line
each with the reason>

## Goals

<what this design delivers. Scope boundaries are the proposal's `## Non-Goals`
and are not repeated here — state one only if the design narrows further than
the proposal did, marked `Narrows scope:`.>

## Architecture

<structure, diagrams welcome; new/changed files, data flow, interfaces>

## Key decisions

<each significant decision + why; each spawns an adr/ draft>

## Error handling

<failure modes and what the design does about them>

## Testing strategy

<how build/verify will prove this — concrete checks, not intentions>

## Grounding

<the design-phase grounding only: the queries and file reads *this design*
rests on. Open-phase grounding stays in proposal.md — do not restate it.>
```

## Rules

- `Status: Confirmed` + `Confirmed:` date are machine-read — first lines
  after the title, exactly as shown. The only other legal status is
  `Status: Under revision` (set by a mid-build design revisit; the
  derivation table routes on it, and re-confirmation replaces it with a
  fresh `Status: Confirmed` + date).
- Every Key decision needs an ADR draft (`references/adr-draft.md`) and
  every behavior change a delta-spec scenario (`references/delta-spec.md`).
- No implementation code in this artifact or this phase.
