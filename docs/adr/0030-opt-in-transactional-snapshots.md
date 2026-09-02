# Opt-in transactional snapshots with semantic checkpoints

- **Status:** Accepted
- **Date:** 2026-09-02

Qualifies [ADR 0004](0004-atomic-writes-state-last.md); plain apply's
partial-success semantics stand unchanged.

## Context

ADR 0004 gives per-file atomicity and state-last persistence, deliberately
allowing partial applies that re-apply convergently. Users asked for a real
undo: apply, regret, revert — without hand-editing. Whole-file backups would
copy resolved secrets into `.homonto/` and clobber unmanaged keys on restore,
both unacceptable. And the current O_EXCL apply lock cannot be reclaimed after
a SIGKILL, which a recoverable transaction needs.

## Decision

We will add opt-in `homonto apply --snapshot`: before the first active write
it prepares a complete mutation inventory (adapter effects plus engine-owned
catalog, remote, lock, state, and GC mutations) and stores semantic
checkpoints — unresolved managed values and content-addressed blobs, never
whole tool files or resolved secrets. A durable journal records each mutation
as prepared, committed, or rolled back; ordinary failures roll back, crashes
leave a discoverable journal that `homonto recover` completes under a process
lock the OS releases on death. `homonto undo` renders a reverse plan, refuses
if any managed after-image changed, and re-resolves secret references
(refusing, not guessing, when resolution fails). Revocation quarantine is
irreversible and never rolled back. Snapshots retain the latest 10.

## Consequences

- Snapshot mode is heavier: prepare-then-commit costs an extra pass.
- Undo restores managed values, not pre-apply drift or historical secret
  bytes — a rotated secret comes back as its current value.
- The apply lock becomes a cross-platform process lock; stale-lock diagnosis
  moves from "remove the file by hand" to "the OS already released it."
- Two new commands (`recover`, `undo`) and a journal format to keep honest.
