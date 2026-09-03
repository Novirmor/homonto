# Receipt destructive close boundaries

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

`close.merged` did not identify the deltas or living-spec images it covered, so
recovery could replay stale requirements over newer text. Archival also preceded
Git integration without recording whether the merge or pull request happened.
An interrupted run could therefore look complete when work was still pending,
and a receipt that named no real merge could mark an archive terminal. Whether
an archive was "tracked" was inferred from sidecar presence, so deleting the
sidecar forged the legacy shape.

## Decision

We will record spec-merge pre/post-images in a versioned workspace sidecar and
record post-archive Git integration in a separate versioned sidecar carrying
one entry per repository in the change's scope (the config repository plus
every declared sibling — a cross-repo change is integrated only when each
repository's branch landed). Receipts fail closed: a merge receipt must resolve
to a real merge commit whose merged-in parent contains the recorded source
commit and which is reachable from the recorded base branch; completion runs
one repository at a time under the shared merge lock. State stamps
`integration_required: true` at close (schema 2), so a tracked archive whose
sidecar is missing or invalid derives `close` and resolves dependencies only
after every repository records completion. Legacy archives — written before the
marker — remain terminal. The newest dated archive generation of a reused name
is authoritative; nothing falls back from a pending newest generation to an
older completed one.

## Consequences

Interrupted spec writes and archive moves have executable, provenance-bound
recovery. Newer living-spec content is never overwritten automatically, and
archival alone no longer hides unfinished integration. Deleting a sidecar is
detectable, cross-repo changes cannot silently complete from one repository,
and a fabricated merge SHA no longer passes. Close gains one command and two
sidecars plus a schema bump; a PR-body handoff remains pending until the PR
actually opens (PR receipts stay externally obtained — the binary checks shape,
not the remote).
