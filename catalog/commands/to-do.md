---
name: to-do
description: Run the to do phase — execute plan.md one task at a time, one implementer then its reviewers.
argument-hint: "The change to work on (optional)"
---

# /to-do

Run to phase 2 (do): execute `plan.md` one task at a time — dispatch
`to-implementer`, verify against the repository, dispatch `to-reviewer`, act
on findings, check off, commit. One implementer at a time (it is the only
agent that edits); read-only reviewers may run concurrently.
Load and follow the `to-do` skill; if it is not installed, tell the user to
install the to framework (declare `[frameworks.to]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `to` binary —
never hand-edit `to-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
