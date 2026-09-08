---
description: Continue a GitHub PR — collect review feedback, drive the matching to/onto change, push verified fixes.
agent: homonto
---

# /h-continue-pr

Load and follow the `h-continue-pr` skill. If it is not installed, say the
`h` framework is missing (declare `[frameworks.h]` and run `homonto apply`)
and stop.

`$ARGUMENTS` is one unambiguous PR reference: a GitHub PR URL,
`OWNER/REPO#NUMBER`, a number, or a unique head branch resolved against the
repository. Resolve its canonical identity rather than requiring a syntax rerun.

The skill feeds the PR's outstanding feedback into the matching workflow
change. With no match, select and explain the workflow by explicit user
preference > existing matching change > repository policy > risk/fit; do not
require a workflow-choice dialog. Continue in the same invocation to verified
fixes pushed to the exact existing PR head, one evidence-backed summary comment,
and honestly addressed threads, unless the user names an earlier endpoint or
asks to pause. The invocation authorizes those actions after verification;
configured tool permissions and nontrusted-feedback scope decisions still apply.
