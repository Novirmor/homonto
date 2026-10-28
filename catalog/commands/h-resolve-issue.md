---
description: Resolve a GitHub issue — spike it, select an installed no/to/onto engine, and deliver a verified PR.
agent: homonto
---

# /h-resolve-issue

Load and follow the `h-resolve-issue` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[workflows.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is the GitHub issue to resolve: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

The skill owns spike, engine selection, execution and publication, including the
record-free `no` route. Continue in the same invocation to its success endpoint
unless the user names an earlier endpoint or asks to pause.
