---
description: Review a GitHub pull request — full context in, findings drafted, nothing posted without approval.
agent: homonto
---

# /h-review-pr

Load and follow the `h-review-pr` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is one unambiguous PR reference: a GitHub PR URL,
`OWNER/REPO#NUMBER`, a number, or a unique head branch resolved against the
repository. Resolve its canonical identity rather than requiring a syntax rerun.

The skill gathers the full context, delegates analysis to the read-only
`h-review` worker, validates the findings, and shows a draft. Nothing is
posted to GitHub until the user explicitly approves the shown draft.
Continue in the same invocation through the draft and publication decision,
then report any approved post URL, unless the user names an earlier endpoint
or asks to pause. A pending decision leaves the draft unpublished; trusted
workspace execution and tool permissions never supply publication consent.
