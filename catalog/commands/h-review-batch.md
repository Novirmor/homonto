---
description: Review a repository's open pull requests in one batch — one h-review worker per PR, drafts first.
agent: homonto
---

# /h-review-batch

Load and follow the `h-review-batch` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is either a repository URL
(`https://github.com/OWNER/REPO`) — optionally followed by `limit N` and/or
`author USER` — or one or more pull request URLs to review explicitly.

The skill reviews each selected PR through a read-only `h-review` worker,
preserves partial results when one PR cannot be fetched, and asks for one
publication decision after presenting every draft. Workers never touch
GitHub.
