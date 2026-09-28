# Resolve no-delta scenarios for every workflow

- **Status:** Accepted
- **Date:** 2026-09-28

## Context

Full documentation-only changes can legitimately omit spec deltas, but the
scenario index admitted task/report declarations only for fix/tweak presets.
Recording accepted unknown IDs, leaving doctor to reject otherwise verified
full changes after they had advanced to close.

## Decision

Choose declaration sources by the presence of delta files, not workflow name.
Any delta files exclusively own scenario IDs, even when their parsed index is
empty. Without delta files, full and preset changes may declare each ID once
in tasks.md or verification.md. Recording rejects unknown or ambiguous IDs
before writing; trace and doctor use the same index.

The explicit no-spec justification remains a workflow review obligation. We
will not infer approval from arbitrary prose, add a state marker, fabricate
specs, or require a downgrade merely to resolve verification scenarios.

## Consequences

Existing no-delta receipts can resolve without migration or deletion. Callers
must declare an ID before recording it; declaration edits can still make a
receipt stale. Task identity, candidate binding, report hashes, verification
scale, and close gates remain unchanged. This extends ADR 0053's declaration
sources beyond presets without weakening delta precedence or evidence history.
