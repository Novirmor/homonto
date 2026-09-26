---
description: Run the onto build phase — plan, then execute reviewable tasks with focused commits.
agent: homonto
---

# /onto-build

Run onto phase 3 (build): follow `onto-build` for full-workflow planning and focused implementation; it routes presets back to their own build step. Pause after planning only when the user explicitly requests it. Continue through verify and close unless the user names an endpoint or asks to pause. If the skill is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
