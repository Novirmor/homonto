---
description: Run the onto build phase — plan, then execute tasks one commit each.
agent: onto
---

# /onto-build

Run onto phase 3 (build): write the implementation plan, choose the build configuration from the work, then execute bite-sized tasks with one commit each. Pause after planning only when the user explicitly requests it. Load and follow the `onto-build` skill, then continue through verify and close unless the user names an endpoint or asks to pause. If the skill is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
