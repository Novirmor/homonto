# Own terminal cleanup in the binaries

- **Status:** Partially superseded by [0086](0086-clean-up-at-close.md) (cleanup runs at close; removal needs no integration into the base)
- **Date:** 2026-10-27

## Context

Terminal workflows leave registered worktrees and abandoned onto directories
reserving names. Existing safe removal primitives do not provide one inventory,
cleanup state or retry procedure. Agent instructions cannot guarantee cleanup.

## Decision

Ship a shared version-1 cleanup inventory, preview and generation-bound apply
contract in homonto, onto and to. Derive eligibility from validated native state
and live bindings, not age, task checkboxes or agent assertions. Apply requires
an exact identity and explicit confirmation, rechecks safety in the binaries,
and records pending/complete receipts separately from immutable archives.
Coordinators inspect this inventory after terminal delivery; cleanup apply keeps
the same protected tool-approval boundary as native worktree removal.

Remove only clean, integrated, identity-validated registered worktrees. Recover
an interrupted removal only from its recorded intent and confirmed Git removal.
Archive clean terminal records left in active directories without rewriting their
state, preserving unsuccessful abandonment and permitting later name reuse.
Keep native branches and all archives. Maintenance does not require reinstalling
an engine merely to clean its terminal records; it grants no lifecycle bypass.

Reject prompt-only cleanup, forced pruning, age-based abandonment and automatic
deletion at completion. Shared no-task allocations retain their existing
`homonto workspace task cleanup` contract; no gains no lifecycle state.

## Consequences

Cleanup is discoverable and resumable without an agent, but removal remains
explicit and can block on dirt, incomplete integration, invalid metadata or
ambiguous identity. Managed history checkpoints only the selected record move
and cleanup receipt. Existing-history mode leaves record edits for a normal
commit. Cleanup completion establishes local resource release, not remote PR
readiness or permission to delete retained branches.
