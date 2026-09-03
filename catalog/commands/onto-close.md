---
description: Run the onto close phase — merge deltas, accept ADRs, and archive the verified change.
agent: onto
---

# /onto-close

Run onto phase 5 (close): validate the close plan, merge spec deltas, number and accept ADR drafts, enforce the guides obligation, then archive and integrate. Load and follow the `onto-close` skill; if it is not installed, tell the user to
install the onto framework (declare `[frameworks.onto]`, then run `homonto
apply`) and stop. Every workflow state change goes through the `onto` binary —
never hand-edit `onto-state.yaml`.

`$ARGUMENTS`, if present, focuses this phase on the described work.
