# Keep history without blocking fresh verification

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

Comparing every historical evidence record with the current report made a
successful re-verification permanently stale. Requiring every selected source
to produce a merge commit also stranded changes that legitimately left a source
unchanged. Deleting history or creating artificial source commits would hide
the problem rather than establish evidence.

## Decision

Keep every evidence record, but use the latest claim for each repository, task,
and scenario as the current obligation. Finalize the report before recording
claims. Scenario declarations must be unique; ambiguous identities provide no
coverage or supersession. No-spec presets declare scenarios in their task or
verification document rather than inventing specification changes.

Integration remains tied to the pinned source candidate. A merge must contain
that exact candidate, with only independently checked bookkeeping descendants
permitted for combined records/source history. An `unchanged:` receipt instead
requires ancestry and tree proof at the receiving commit. Explicit PR receipts
record a locally checked publication head, but remain external assertions, not
proof that the remote host delivered the code.

## Consequences

Fresh verification can clear stale current claims without erasing prior failures.
Unchanged sources can finish without synthetic merges or empty pull requests.
Callers must identify publication candidates explicitly and must recheck actual
remote delivery. Historical records remain available for audit, but are no
longer all treated as simultaneous current verification obligations.
