---
name: no
description: Use for the smallest development-and-review loop. The model decides the work shape and verification; it delegates implementation and review, without a state machine or companion binary.
---

# no

Work directly from the user's request and the repository's existing rules. Do
not create workflow state, plans, phases, or receipts. Decide the smallest
useful approach from the task and surrounding code rather than asking for a
ceremony choice.
Follow the shared [workspace policy](../homonto/references/workspace-policy.md)
and [autonomy policy](../homonto/references/autonomy.md).

1. Inspect the relevant code and state the concrete outcome to achieve.
2. Dispatch `no-implementer` with the outcome, scope, working directory, and
   any constraints the task supplies. It is the only writer.
3. Inspect the returned diff and verification. Dispatch `no-reviewer` with the
   request, diff, and results.
4. Resolve substantive review findings yourself: return focused fixes to the
   implementer and re-review when needed. Decide appropriate verification from
   the change; report exactly what ran and any remaining gap.

The two subagents do not delegate. Do not invoke `onto` or `to` commands or
binaries, and do not emulate their files, state, or phase transitions. Keep
decisions in the conversation and let the task determine how much detail is
needed.
