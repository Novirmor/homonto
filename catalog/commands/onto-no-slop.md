---
description: Strip predictable AI writing tells from onto prose artifacts.
agent: onto
---

# /onto-no-slop

Remove AI writing patterns from an onto prose artifact — proposal, design, ADR, guide, verification report, or commit message. Load and follow the `onto-no-slop` skill; if it is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
