---
description: Continue a GitHub PR — collect review feedback, drive the matching to/onto change, push verified fixes.
agent: homonto
---

# /h-continue-pr

Load and follow the `h-continue-pr` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is the GitHub pull request to continue:
`https://github.com/OWNER/REPO/pull/NUMBER`.

The skill feeds the PR's outstanding feedback into the matching workflow
change (resuming it, or asking which kind to open when none matches), pushes
to the PR branch only after verification passes, and comments with evidence.
