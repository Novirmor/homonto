---
disable-model-invocation: true
---

# onto bypass

This skill is available only after a user explicitly invokes `/onto-bypass`.
Do not load it from another skill, suggest it as a normal workflow step, or use
it to avoid unfinished work. The user must name the target and provide the
reason in their own request.

`onto bypass` skips normal workflow gates and records a permanent audit entry.
It still refuses a missing framework, invalid change name, unreadable state, or
filesystem/archive error.

```sh
onto bypass <change> --to <open|design|build|verify|close|archive> --reason "<user reason>"
```

`archive` moves the workspace without merging pending spec deltas or ADRs.
Explain that outcome before executing the command. Do not invent, shorten, or
reuse the user's reason.
