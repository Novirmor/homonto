---
name: to-bypass
description: Explicit user-only bypass for to workflow gates.
argument-hint: "<change> --to <plan|do|done|archive> --reason <reason>"
---

# /to-bypass

Use this command only when the user explicitly asks to bypass the to workflow.
Load the `to-bypass` skill and follow it. Do not invoke this command from
another skill or ordinary workflow step.

The user must provide a change, target, and their own non-empty reason. Explain
that `done` and `archive` record `verified: false`, then run:

```sh
to bypass <change> --to <plan|do|done|archive> --reason "<user reason>"
```
