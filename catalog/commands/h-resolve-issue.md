---
description: Resolve a GitHub issue — spike it, pick to or onto, drive the workflow, open a verified PR.
agent: homonto
---

# /h-resolve-issue

Load and follow the `h-resolve-issue` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[frameworks.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is the GitHub issue to resolve: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

Continue in the same invocation to one verified PR closing the issue and a
completed workflow record, unless the user names an earlier endpoint or asks
to pause. The skill spikes first, selects and explains the workflow by explicit
user preference > existing matching change > repository policy > risk/fit,
and publishes only after verification passes. No mandatory workflow-choice
dialog or second conversational push/PR approval is needed; configured tool
permissions and genuine material-intent questions still apply.
