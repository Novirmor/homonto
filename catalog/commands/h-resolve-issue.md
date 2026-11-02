---
description: Resolve an issue (or a Bitbucket brief) — spike it, select an installed no/to/onto engine, and deliver a verified PR.
agent: homonto
---

# /h-resolve-issue

Load and follow the `h-resolve-issue` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the
hosting workflow for the target's host (declare `[workflows.h-workflow-gh]`,
`[workflows.h-workflow-gl]` or `[workflows.h-workflow-bb]` if needed and run
`homonto apply`), and stop. The target's host skill (`h-host-gh`, `h-host-gl`
or `h-host-bb`) supplies that host's forms of every reference below.

`$ARGUMENTS` is the GitHub issue to resolve: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

The skill owns spike, engine selection, execution and publication, including the
record-free `no` route. Continue in the same invocation to its success endpoint
unless the user names an earlier endpoint or asks to pause.
