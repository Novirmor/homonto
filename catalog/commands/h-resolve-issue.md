---
description: Resolve a GitHub issue — spike it, pick to or onto, drive the workflow, open a verified PR.
agent: homonto
---

# /h-resolve-issue

Load and follow the `h-resolve-issue` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is the GitHub issue to resolve: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

The skill always spikes first, always asks the user to choose `to` or
`onto`, and pushes or opens a pull request only after the chosen workflow's
verification has passed.
