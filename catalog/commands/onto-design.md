---
description: Run the onto design phase — explore approaches and write design.md.
agent: onto
---

# /onto-design

Run onto phase 2 (design): explore viable approaches, select one from evidence and user-owned constraints, then write design.md plus ADR drafts and spec deltas. Load and follow the `onto-design` skill, then continue through the remaining phases unless the user names an endpoint or asks to pause. If the skill is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
