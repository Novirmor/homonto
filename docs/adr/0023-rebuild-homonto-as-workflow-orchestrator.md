# Rebuild Homonto as a workflow orchestrator

- **Status:** Accepted
- **Date:** 2026-08-24

## Context

Homonto currently combines a general AI-tool configuration projector with two
separate coding-workflow binaries. The products compete for identity, duplicate
runtime behavior, and create a security and maintenance surface larger than the
value of the experiment. Preserving their commands and state would constrain a
coherent replacement around defects and abstractions that are no longer wanted.

## Decision

We will replace the existing product with one Go binary that provides explicit
Task and Change workflow state machines. Task is a low-documentation
`plan -> do -> done` path. Change is a governed
`open -> design -> build -> verify -> close` path with Fix, Tweak, and Full
variants. Both use binary-owned state and guards, CLI-run verification, native
host subagents, isolated Git worktrees or non-Git snapshots, portable
checkpoints, and thin Claude Code and OpenCode integrations.

The replacement is a clean break. We will remove general resource projection,
the separate `onto` and `to` binaries, old workflow formats, and compatibility
code. We will implement the two workflows explicitly rather than adopting a
generic workflow language or Comet-compatible runtime. The approved detailed
design is [`../homonto-workflow-redesign.md`](../homonto-workflow-redesign.md).

## Consequences

Homonto gains one product promise and one hardened runtime for delegation,
evidence, recovery, and host integration. Task and Change can share mechanics
without sharing ambiguous state-machine policy.

The cost is a full breaking rewrite: current users cannot migrate their config
or active workflow state, most existing code and documentation will be removed,
and the first release must implement and verify the entire replacement before
publication. Supporting maximal parallel worktrees, multiple member
repositories, cross-machine resume, and signed self-update also makes the new
runtime substantial even though its product scope is narrower.
