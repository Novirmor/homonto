# Promote a `to` change into onto

- **Status:** Accepted
- **Date:** 2026-09-02

Reverses the no-escalation decision recorded in
[`docs/to-framework-design.md`](../to-framework-design.md) ("a `to` change
cannot be promoted into an onto change; the documented answer is redo it by
hand"). Framework exclusivity itself stands.

## Context

The no-escalation rule assumed framework choice is per-repository and static.
In practice work grows: a `to` task acquires design questions, evidence
obligations, or a second reader, and the recorded answer — redo everything as
an onto change, discarding the plan and history — throws away exactly the
record `to` exists to keep. The cost of the rule is paid at the moment it
hurts most.

## Decision

We will add `to promote <name> [--as <name>] --yes`: it creates a full onto
change in phase `open` (a fresh proposal seeded from the imported plan) and
moves the complete `to` workspace, unchanged, under
`docs/changes/<name>/imported-to/`. Promotion does not claim design or
verification happened — phase starts at `open`. It takes the `to` workspace
lock and a destination lock shared with `onto new`, in that fixed order, and
recovers idempotently from a crash by re-deriving generated files and
verifying imported bytes. It never installs both frameworks: the printed next
steps are the `homonto.toml` swap, `homonto apply`, and `/onto`.

## Consequences

- A growing task keeps its plan and history across the framework boundary.
- `to` gains one command that depends on onto's state format; the formats
  themselves stay separate, and promotion is one-way.
- The exclusive-choice rule moves from "never cross" to "never hold both":
  a repository still runs exactly one framework at a time.
