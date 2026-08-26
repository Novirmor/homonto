# Journal lease recovery around an on-disk commit marker

- **Status:** Accepted
- **Date:** 2026-08-24

## Context

Active work holds one lease file per member, and a crash can leave an
acquisition half-finished with no way to tell, from the journal alone,
whether the work ever became active. The journal can also diverge from the
filesystem (a lease applied but its row uncommitted, or a foreign lease
appearing on a not-yet-applied target). Recovery needs a durable commit
point plus a rule for which way to converge.

## Decision

Lease recovery is governed by an on-disk checkpoint commit marker — a
sentinel file `.homonto/leases/<work-id>.active` written atomically as the
last file effect of an acquisition, listing the acquired leases but never
the recovery tokens. Task 5 replaces it with the real checkpoint; until
then it is the commit point. The recovery policy per pending acquisition:

- The marker's **presence** forbids rolling the activation back, even when
  its journal row is uncommitted (unrecorded window); recovery rolls
  forward and finishes the projection. The marker only counts when its
  content names the operation being recovered — a sibling acquisition that
  lost the O_EXCL race must not roll forward onto the winner's marker, and
  rolls its own token-matching leases back instead.
- Without the marker, recovery rolls forward when every remaining lease is
  still acquirable and rolls back when a foreign lease blocks one —
  removing exactly the leases whose content and tokens match the journal.
- The expected tokens live in the operation payload and the per-effect
  payloads of the journal (the recorded token store), never in the
  sentinel or any committed artifact. PID liveness is diagnostic only —
  a reused pid can look alive, and no timeout-based reclamation exists.
- A membership rescan during active work converges forward after a crash
  (the membership change completes) but rolls back on an in-process
  failure (the caller sees the partial change undone). The marker is
  rewritten on every membership change: its version field is bumped to
  mark downstream evidence stale, and its lease list is updated to the
  post-change set (added members appended, released members dropped), so
  the marker always describes the leases actually held — a later rescan
  can release a member added by an earlier one.

## Consequences

Roll-forward is the trusting default everywhere except a live conflict
before the marker; roll-back is exact-token only, so a foreign lease is
never removed by recovery. The op-scoped marker check makes concurrent
same-work acquisitions converge to one winner without corrupting the other.
Until Task 5 lands, the sentinel is an extra artifact to clean up at
archive, and its version bump is only an invalidation marker — the engine
side has no evidence graph yet. A crash in a release after some removals
rolled back can leave a lease re-created that the caller believes released;
re-running the release converges.
