---
description: Finish a to change with final-candidate verification and review, the documented unavailable-dispatch fallback when needed, then archive and integrate.
agent: homonto
---

# /to-done

Load and follow the `to-done` skill; if it is not installed, tell the user to install the to
framework (declare `[engines.to]`, then run `homonto apply`) and stop.
The skill owns final verification, review, archival and delivery recovery.
Continue to its success endpoint in the same invocation, including any pending
authorized integration/publication after archive, unless the user names an
earlier endpoint or asks to pause.

`$ARGUMENTS`, if present, focuses this phase on the described work.
