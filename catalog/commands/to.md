---
name: to
description: Start or resume the to minimal coding workflow for this repo.
argument-hint: "What to work on (optional; omit to resume the active change)"
---

# /to

Drive the **to** three-phase workflow (plan → do → done) for this repository.
If the `to` skill is installed, load and follow it; otherwise tell the user the
to framework is not installed and stop (install it with `homonto apply` after
declaring `[frameworks.to]`).

The `to` skill is the dispatcher — it checks the `to` binary (`to version`),
finds the active change via `to status --json`, and routes to the matching
sub-skill (`to-plan`, `to-do`, or `to-done`). Every state change goes through
the `to` binary — never hand-edit `to-state.yaml`. The read-only subagents
(`to-explorer`, `to-reviewer`, `to-skeptic`) are dispatched concurrently;
`to-implementer` runs strictly one at a time, because it is the only agent
that edits and `to` keeps a single working tree.

`$ARGUMENTS`, if present, describes what to work on — use it to open a new
change or to focus the current phase. If absent, resume the active change.
