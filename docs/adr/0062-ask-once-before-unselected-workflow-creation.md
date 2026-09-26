# Ask once before creating an unselected workflow change

- **Status:** Accepted
- **Date:** 2026-09-26

## Context

The shared coordinator previously picked `to` or `onto` from task fit and
proceeded without asking. Bounded bugs and small edits can fit both `to` and
onto's `fix` or `tweak` presets, but that default often selected `to` without
letting the user choose the evidence-backed, handoff-ready record. Asking at
each phase instead would turn normal continuation into repeated approval.

## Decision

Before creating a change with no explicit workflow path, the coordinator
recommends a path and asks once among viable `to`, `onto fix`, `onto tweak`,
and full `onto` choices. `/onto` selects the onto family, leaving the preset
choice open unless specified; explicit path commands and stated choices do not
need another prompt. Existing matching changes resume in their recorded
workflow. Repository policy and preset limits constrain the offered choices;
they cannot be waived by selecting a shorter path. GitHub issue/PR intake uses
the same choice after research and before creating an unmatched change.

## Consequences

- New work without an explicit path pauses for one user decision even when a
  technical default looks obvious. The owner chooses the record's rigor; the
  coordinator still recommends based on scope and risk.
- Resumes, phase transitions, and preset upgrades do not repeat this prompt.
  An answer does not authorize publication or bypass verification.
- This reverses the automatic selection policy in ADR 0051 for new changes;
  its trusted-execution and continuation rules otherwise remain unchanged.
