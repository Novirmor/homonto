---
description: Run the to done phase — real verification, at least one completed skeptic pass on the final candidate, then `to done --verified` archives the change.
agent: to
---

# /to-done

Run to phase 3 (done): run the plan's `Final Verify:` command, dispatch
one or more `to-skeptic` passes concurrently against the unchanged final
candidate, triage their findings, record the outcome in `plan.md`, then finish with
`to done <name> --verified`. The
`--verified` flag is self-asserted by design — this skill is what makes the
assertion true before it is made; never pass it early. Commit the resulting
archived directory and terminal state. Load and follow the
`to-done` skill; if it is not installed, tell the user to install the to
framework (declare `[frameworks.to]`, then run `homonto apply`) and stop.

`$ARGUMENTS`, if present, focuses this phase on the described work.
