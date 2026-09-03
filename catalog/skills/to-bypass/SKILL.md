---
disable-model-invocation: true
---

# to bypass

This skill is available only after a user explicitly invokes `/to-bypass`.
Do not load it from another skill, suggest it as a normal workflow step, or use
it to avoid unfinished work. The user must name the target and provide the
reason in their own request.

`to bypass` skips normal workflow gates and records a permanent audit entry.
It still refuses a missing framework, invalid change name, unreadable state, or
filesystem/archive error.

```sh
to bypass <change> --to <plan|do|done|archive> --reason "<user reason>"
```

`done` and `archive` archive the workspace with `verified: false`. Do not
invent, shorten, or reuse the user's reason.
