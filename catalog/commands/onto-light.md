---
description: onto light preset — bounded bug fixes and small edits, verified without skeptics.
agent: homonto
---

# /onto-light

Run the onto-light preset for a bounded bug fix or small non-bug change. Load and
follow the `onto-light` skill for classification, eligibility, verification, and
continuation through close; respect an explicit endpoint or pause. If it is not
installed, tell the user to install the onto framework (declare `[engines.onto]`,
then run `homonto apply`) and stop. Every workflow state change goes through the
`onto` binary — never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
