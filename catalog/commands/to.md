---
description: Start or resume the to minimal coding workflow for this repo.
agent: homonto
---

# /to

Drive the **to** three-phase workflow (plan → do → done) for this repository.
If the `to` skill is installed, load and follow it; otherwise tell the user the
to framework is not installed and stop (install it with `homonto apply` after
declaring `[engines.to]`).

The skill owns discovery of active work and interrupted archived delivery,
phase routing and repository-scoped execution. Continue to its success endpoint
in the same invocation unless the user names an earlier endpoint or asks to pause.
Every state change goes through the `to` binary — never hand-edit `to-state.yaml`.

`$ARGUMENTS`, if present, describes what to work on — use it to open a new
change or to focus recovery. If absent, follow the skill's identity discovery.
