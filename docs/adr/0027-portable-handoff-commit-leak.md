# Document the portable-handoff commit leak and force-takeover semantics

- **Status:** Accepted
- **Date:** 2026-08-24

## Context

The portable handoff (`PreparePortable`) and attach (`Attach`) operations
journal a control-repository commit as their last effect, and the forced
takeover re-marks a consumed checkpoint before re-consuming it. Git commits
cannot be undone, and the checkpoint state machine forbids re-consuming a
consumed generation, so two things need pinning down: what a rolled-back
operation leaves behind, and what a forced takeover may and may not do.

The commit effect stages only the portable artifacts
(`.homonto/checkpoint.json`, `.homonto/config.toml`, and the
`docs/homonto` subtree when present) — never the whole repository — so an
unrelated dirty file never rides a handoff commit, and the same pathspec
scope is what makes the diff check honest.

## Decision

- **The commit effect's Revert is a documented no-op leak.** A rolled-back
  attach or handoff may leave its commit; the checkpoint revert restores
  the working tree, which is exactly what the next attempt diffs against.
  Two leak classes are accepted: the ADR 0025 unrecorded-window leak (the
  commit performed, its applied row never committed, roll-back closes the
  row without a Revert), and the recorded-applied class (the row reads
  applied, roll-back calls Revert, the no-op succeeds, and the row is
  journaled `reverted` — the journal asserts a lie). The lie is accepted
  because the working-tree checkpoint, not HEAD, is what the next attach
  or handoff converges on, and a leaked commit's content is exactly what a
  converged operation commits anyway.
- **Force takeover** applies only to a consumed checkpoint: re-mark it
  transferable at generation +1 with a fresh transfer id (the only legal
  way out of consumed — `ValidateTransition` demands the bump and forbids
  double-consume), record a `forced_takeover` decision (the decisions table
  has no kind column; the summary encodes it) and the `evidence_stale`
  meta key, then consume at the bumped generation. The takeover is an
  effect like any other: a rolled-back force attach restores the consumed
  checkpoint at the original generation, deletes the decision row, and
  deletes the evidence marker.
- **Force on an already-transferable checkpoint is a normal attach.** The
  takeover bump is the remedy for consumption elsewhere, not a mode flag;
  a transferable checkpoint needs no remedy.
- **`PortableRequest.TransferID`** is an optional deterministic override of
  the minted transfer id, for recovery tooling and tests that must
  reproduce a known transition. It is the only way to reach a diff-free
  prepare, whose required commit then refuses with `ErrNothingToCommit` —
  including during recovery re-apply, where the operation stays prepared (a
  diagnosable stuck state) rather than converging. Production never mints
  predictably, so production cannot reach the stuck state.

## Consequences

A rolled-back attach may leave a `homonto: attach <gen>` commit whose
checkpoint was never consumed on disk; operators read the checkpoint and
the journal, not HEAD, to know what attached. The commit-leak tests pin
both leak classes and the bytes-coincide re-attach (no second commit).
Force takeover doubles the checkpoint write and adds decision/evidence
rows; the force crash matrix pins convergence at every one of the eleven
force effects.

Single-hop is the default: the checkpoint state machine admits exactly one
transferable→consumed hop, and the consumed→transferable re-mark is
reachable only through the human-initiated forced takeover. A
human-confirmed re-handoff command is future work (WS4).
