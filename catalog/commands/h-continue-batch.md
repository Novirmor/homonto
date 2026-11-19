---
description: Continue a batch of GitHub pull requests, accounting for their relationships.
agent: homonto
---

# /h-continue-batch

Load and follow the `h-continue-batch` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[workflows.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is a repository URL, optionally followed by `limit N` and/or
`author USER`, or one or more unambiguous PR references. Ask whether the
changes are independent or connected unless the user already specified this.
Continue in the same invocation to the skill's success endpoint unless the
user names an earlier endpoint or asks to pause.
