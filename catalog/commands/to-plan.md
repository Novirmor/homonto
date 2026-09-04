---
description: Run the to plan phase — write plan.md as bite-sized, verifiable tasks.
agent: to
---

# /to-plan

Run to phase 1 (plan): write `<workflow-root>/tasks/<name>/plan.md` — a short goal
statement plus a checklist of bite-sized tasks, each stating a concrete
outcome, files and symbols, behavioral change, and exact verification command
with its passing signal — then advance with `to phase <name>` and continue
through do and done unless the user names an endpoint or asks to pause. Load and follow
the `to-plan` skill; if it is not installed, tell the user to install the to
framework (declare `[frameworks.to]`, then run `homonto apply`) and stop.
Every workflow state change goes through the `to` binary — never hand-edit
`to-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
