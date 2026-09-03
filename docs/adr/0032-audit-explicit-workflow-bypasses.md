# Audit explicit workflow bypasses

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

Workflow gates are deliberately strict, but an operator may need to recover or
archive a change when its ordinary evidence cannot be completed. A hidden flag
or an ordinary skill escape hatch would make that action easy to invoke without
a durable explanation.

## Decision

We will provide separate `onto bypass` and `to bypass` commands, exposed only
through dedicated manual-only skills and slash commands. They accept an
explicit target and non-empty user reason, skip workflow gates, and append a
versioned `.onto/bypass.json` or `.to/bypass.json` record. The record carries
the timestamp, command, source and target, reason, and skipped gates. Archive
bypasses move the unmerged workspace intact.

## Consequences

- Operators can recover a blocked workflow without editing state by hand.
- Audit data survives archival and is not lost when an older binary rewrites
  the workflow state file.
- OpenCode can prevent model invocation of the dedicated skills, but cannot
  prove which person typed a shell command; binary access remains an operator
  trust boundary.
