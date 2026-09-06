---
description: Review a GitHub pull request — full context in, findings drafted, nothing posted without approval.
agent: homonto
---

# /h-review-pr

Load and follow the `h-review-pr` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is the GitHub pull request to review:
`https://github.com/OWNER/REPO/pull/NUMBER`.

The skill gathers the full context, delegates analysis to the read-only
`h-review` worker, validates the findings, and shows a draft. Nothing is
posted to GitHub until the user explicitly approves the shown draft.
