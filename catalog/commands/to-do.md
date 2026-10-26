---
description: Run the to do phase — execute independent repositories in parallel, with serial writers and candidate review per checkout.
agent: homonto
---

# /to-do

Load and follow the `to-do` skill, then continue through done unless the user
names an endpoint or asks to pause. If the skill is not installed, tell the user to
install the to framework (declare `[engines.to]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `to` binary —
never hand-edit `to-state.yaml`. The skill owns repository-scoped concurrency,
review and parent-owned commits.

`$ARGUMENTS`, if present, focuses this phase on the described work.
