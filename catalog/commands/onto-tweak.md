---
description: onto small-change preset for copy, config, docs, and tiny features.
agent: onto
---

# /onto-tweak

Run the onto-tweak preset for a small non-bug change (≤5 files, no new capability): open-lite, lightweight build, light verify, and close in one invocation. Load and follow the `onto-tweak` skill; if it is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
