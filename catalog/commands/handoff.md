---
description: Write a handoff document so a fresh agent can continue this work.
---

# /handoff

Compact this conversation into a handoff document for a fresh agent with no prior
context. If the `handoff` skill is installed, follow it; otherwise apply the same
contract below.

When `references/tmp.md` names a declared workspace tmp directory, write
`handoff-<YYYYMMDD-HHMMSS>.md` there. Otherwise return the handoff in
conversation; do not require an external-directory write. Write for a reader
who has the repo but none of this chat. Capture decisions and their reasons, not
a transcript. Reference durable artifacts (specs, plans, ADRs, issues, commits,
diffs) by path or URL rather than restating them.

Cover: **Goal**, **Current state**, **Done** (with commit shas / paths),
**Next** (ordered, specific), **Key files & pointers**, **Open questions &
decisions** (with rationale), **Gotchas**, and **Suggested skills**.

Keep it self-contained, redact any secrets, and print the saved path when one
was written.

`$ARGUMENTS`, if present, describes the next session's focus — weight the Next and
Key-files sections toward it.
