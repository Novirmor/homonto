# Describe engines instead of naming them

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

Workspace tasks accepted only `no`, `onto` and `to`, and only from a
`builtin:` source. Every task decision branched on the name: `t.Engine ==
"no"` meant "owns no lifecycle, allocates task-owned worktrees", and anything
else meant "binds checkouts through the native lifecycle" (ADR 0068). An
external engine could not be selected, and the meaning of each branch lived
only in the code.

## Decision

A workspace task reads the selected engine's schema 2 `[engine]` contract
(ADR 0092) and branches on its properties:

- `lifecycle = "stateless"` replaces `== "no"`: no lifecycle change, task-owned
  worktrees. `lifecycle = "native"` replaces the onto/to branches: a required
  `--change`, checkouts bound through the worktree registry keyed by the
  engine name.
- `operations` gate the task commands: `start` for create, `resume` for every
  load, `integration-check` for `task check`, `release` for `task cleanup`.
- The task records the lifecycle it was created under. Resume refuses a task
  whose engine now declares a different one. A task written before this field
  existed adopts the current descriptor, because only builtins could create
  one.
- Any installed engine with a schema 2 contract can be selected. A
  `lifecycle = "native"` engine must still be builtin: an external engine has
  no way yet to report its lifecycle state to the coordinator. That reporting
  surface is the worktree registry binding onto and to already write, and #67
  opens it to external engines.

We rejected keeping the name checks and adding a parallel path for external
engines: two models of the same decision drift, and the builtins would never
exercise the external one.

## Consequences

`no`, `onto` and `to` run unchanged as cases of the model, and the tests pin
them that way. A stateless external engine can run workspace tasks today. A
contract change to an installed engine fails resume loudly instead of
reinterpreting recorded checkouts. The pre-descriptor adoption is the one
read of an older task shape. It goes away when task records next change
version.
