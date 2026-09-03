---
name: onto-bypass
description: Explicit user-only bypass for onto workflow gates.
argument-hint: "<change> --to <phase|archive> --reason <reason>"
---

# /onto-bypass

Use this command only when the user explicitly asks to bypass the onto
workflow. Load the `onto-bypass` skill and follow it. Do not invoke this command
from another skill or ordinary workflow step.

The user must provide a change, a target or `archive`, and their own
non-empty reason. Explain that `archive` preserves unmerged deltas and ADRs in
the archived workspace, then run:

```sh
onto bypass <change> --to <open|design|build|verify|close|archive> --reason "<user reason>"
```
