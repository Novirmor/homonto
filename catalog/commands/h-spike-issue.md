---
description: Spike a GitHub issue — read-only research and an implementation brief, no code changes.
agent: homonto
---

# /h-spike-issue

Load and follow the `h-spike-issue` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is the GitHub issue to spike: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

This command is research. It never edits code, creates branches, starts
workflow state, pushes, or posts to GitHub.
Continue in the same invocation to a citation-checked implementation brief
with risks and workflow fit, unless the user names an earlier endpoint or
asks to pause. An explicitly requested closed issue needs no second approval.
