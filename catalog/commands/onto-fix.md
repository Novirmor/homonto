---
description: onto bug-fix preset — reproduce with a failing test, fix, verify, close.
agent: onto
---

# /onto-fix

Run the onto-fix preset for a behavior fix that needs no new capability: open-lite, build from a failing reproduction test, verify, and close in one invocation. Load and follow the `onto-fix` skill; if it is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
