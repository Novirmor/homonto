---
description: Spike a GitHub issue — research and an implementation brief, optionally posted as an approved issue comment; no code changes.
agent: homonto
---

# /h-spike-issue

Load and follow the `h-spike-issue` skill. If it is unavailable, say the
required skill is unavailable, direct the user to install or reapply the `h`
GitHub skill bundle (declare `[frameworks.h]` if needed and run `homonto apply`),
and stop.

`$ARGUMENTS` is the GitHub issue to spike: one issue URL
(`https://github.com/OWNER/REPO/issues/NUMBER`) or `OWNER/REPO#NUMBER`.

This command is research. It never edits code, creates branches, starts
workflow state, or pushes. After presenting the brief, offer to post it as an
issue comment; posting requires explicit approval of the shown draft. Pending
or declined approval leaves it unpublished. Skip the offer if the user requested
no posting.
Continue in the same invocation to a citation-checked implementation brief
with risks and workflow fit and the optional publication outcome, unless the user
names an earlier endpoint or asks to pause. An explicitly requested closed issue
needs no second approval to research; posting still requires draft approval.
