---
name: no
description: Provides no subagents for minimal implementation, review and final skepticism.
---

# no

`no` provides three subagents:

- `no-implementer` — the only writer; implements an assigned change and runs
  its assigned verification.
- `no-reviewer` — read-only; reviews an assigned change and reports findings.
- `no-skeptic` — read-only; attacks final-candidate claims and evidence.

Parents invoking these workers follow [task execution](references/execution.md),
also used by `/no` and GitHub workflow callers. This inventory adds no lifecycle.
