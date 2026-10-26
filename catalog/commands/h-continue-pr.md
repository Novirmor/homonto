---
description: Continue a GitHub PR — address feedback, verify fixes, and update the same PR without requiring an engine.
agent: homonto
---

# /h-continue-pr

Load and follow the `h-continue-pr` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[workflows.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is one unambiguous PR reference: a GitHub PR URL,
`OWNER/REPO#NUMBER`, a number, or a unique head branch resolved against the
repository. Resolve its canonical identity rather than requiring a syntax rerun.

The skill owns feedback scope, safe checkout, verification and publication.
It is engine-optional: do not select an engine or create lifecycle records unless
the user explicitly selects that route. Continue in the same invocation to the
skill's success endpoint unless the user names an earlier endpoint or asks to
pause.
