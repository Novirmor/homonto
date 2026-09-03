---
description: Explicit user-only bypass for to workflow gates.
agent: to
---

# /to-bypass

Use this command only when the user explicitly asks to bypass the to workflow.
This command contains the complete contract; there is deliberately no
discoverable bypass skill. Do not invoke or suggest it from another skill or
ordinary workflow step.

The user must provide a change, target, and their own non-empty reason. Explain
that `done` and `archive` record `verified: false`, then run:

```sh
to bypass <change> --to <plan|do|done|archive> --reason "<user reason>"
```
