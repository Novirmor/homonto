# onto's axis is handoff and audit, not "enterprise"

- **Status:** Superseded by 0023
- **Date:** 2026-07-27

## Context

onto was positioned as "enterprise tooling" against `to`'s "simple
development". A review took that claim seriously and found nothing behind it:
`onto-state.yaml` has no author, no reviewer, no approver, and there is no CI
verb that fails a build on an unmet gate. Measured against what "enterprise"
usually means — identity, roles, attributed approvals, enforcement off the
developer's laptop — the word was unearned, and no amount of further prose
would have earned it.

The maintainer's actual meaning was different and narrower: after the
procedure, **someone else can pick the work up or check what was done**, with
git and the usual tools assumed. That is a property onto largely already had
and had never stated, while spending its positioning on a claim it could not
support.

The gap that mattered was elsewhere. ADR 0018 made `tasks.md` the single
checkoff paired to `plan.md` by task number — which *is* the resume
mechanism — but the correspondence was checked only by a prose item in the
close-phase lint checklist. Drift between the two files was therefore caught
late, or not at all, in the framework whose whole pitch is that the binary
guarantees things the prose cannot.

## Decision

onto's axis is that a change survives being handed to someone who was not
there. `to` matches onto's *code* standards and skips its *record*; company
size is not the distinction and is no longer claimed.

Four things carry the claim, and the docs now name exactly these: the derived
phase (`onto state --json`), the recovery pack (`onto handoff`), the recorded
gate answers (the evidence tokens plus `notes.md`), and `verification.md`'s
literal command output. **Who** answered and **when** come from git over
`docs/changes/<name>/` — onto stores no identity of its own, because that
would be a second and weaker copy of what the VCS already guarantees.

The resume mechanism is enforced by the binary. `onto doctor` now reports
`tasks.md` ↔ `plan.md` drift: a task number in one file and not the other, or
any checkbox in `plan.md`. A change with no `plan.md` is a preset, not drift.
The close-phase checklist now says to run `onto doctor` rather than to eyeball
the pairing.

Two artifact-precision rules follow from the same axis, since a record a
stranger reads must have one place per fact: **grounding** has two owners
(`proposal.md` for open, `design.md` for design) and `notes.md` keeps none;
scope **Non-Goals** belong to `proposal.md` alone, and `design.md` records a
boundary only when it narrows further.

## Consequences

- A workspace with drifted task files now fails `onto doctor`, including the
  `--quiet` enforcement hook. Repos carrying existing drift will see it on the
  next run; that is the unreported problem surfacing, not a regression.
- The check is structural, not semantic. It proves every task number appears in
  both files — never that the detail block describes the task.
- onto no longer promises anything about roles, approvals, or CI enforcement.
  If those are ever wanted they are new work with a new threat model, not a
  reading of what ships today (T-honest still assumes an honest agent).
- Grounding recorded in `notes.md` by an older change stays readable; nothing
  migrates. The templates simply stop asking for a third copy.
