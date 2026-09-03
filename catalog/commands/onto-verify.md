---
description: Run the onto verify phase — check the change against design and every spec scenario.
agent: onto
---

# /onto-verify

Run onto phase 4 (verify): pick a verification level from the change scale, check the implementation with fresh evidence, and write verification.md. Load and follow the `onto-verify` skill, then continue through close when verification passes unless the user names an endpoint or asks to pause. If the skill is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
