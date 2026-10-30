---
description: Resolve only an existing GitHub PR's merge conflicts, verify the resolution, and push to the same head without requiring an engine.
agent: homonto
---

# /h-resolve-conflicts

Load and follow the `h-resolve-conflicts` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[workflows.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is one unambiguous PR reference: a GitHub PR URL,
`OWNER/REPO#NUMBER`, a number, or a unique head branch resolved against the
repository. Resolve canonical identity rather than requiring a syntax rerun.

The skill owns the conflict-only boundary, safe checkout, merge, verification
and same-PR push. Do not turn this into review-feedback work or select an engine.
Continue in the same invocation to its success endpoint unless the user names
an earlier endpoint or asks to pause. Broader work needs explicit scope approval
through the native question dialog before edits; a no-conflicts result is a no-op.
