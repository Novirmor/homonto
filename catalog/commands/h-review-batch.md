---
description: Review a repository's open pull requests in one batch — one h-review worker per PR, drafts first.
agent: homonto
---

# /h-review-batch

Load and follow the `h-review-batch` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[frameworks.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is either a repository URL
(`https://github.com/OWNER/REPO`) — optionally followed by `limit N` and/or
`author USER` — or one or more unambiguous PR references (PR URLs,
`OWNER/REPO#NUMBER`, numbers, or unique head branches resolved against the
repository). An explicit count or list above ten needs no duplicate count approval.

The skill reviews each selected PR through a read-only `h-review` worker,
preserves partial results when one PR cannot be fetched, and asks for one
publication decision after presenting every draft. Workers never touch
GitHub.
Continue in the same invocation until every selected PR has a validated draft
or explicit failure, the publication decision is handled, and the final counts
and any posted URLs are reported, unless the user names an earlier endpoint or
asks to pause. Pending approval leaves drafts unpublished; execution trust
never authorizes review publication.
