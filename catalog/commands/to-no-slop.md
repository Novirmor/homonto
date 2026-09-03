---
description: Remove AI writing patterns from to prose artifacts (plans, notes, commit messages).
agent: to
---

# /to-no-slop

Apply the `to-no-slop` skill to the prose at hand: plans, execution notes,
verification records, commit messages. Load and follow the `to-no-slop`
skill; if it is not installed, tell the user to install the to framework
(declare `[frameworks.to]`, then run `homonto apply`) and stop. Never edit
machine-read markers (task checkboxes, literal verify commands and their
output) — those are contract, not prose.

`$ARGUMENTS`, if present, names the artifact or text to de-slop.
