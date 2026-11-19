---
name: h-continue-batch
description: Use when the user asks to continue or address feedback on multiple GitHub pull requests together, including /h-continue-batch.
---

# h-continue-batch

Continue selected PRs using the existing `h-continue-pr` process for each one,
with a batch-wide view of their relationships. The invocation authorizes the
same per-PR actions as `h-continue-pr` for the selected PRs, not force-pushes,
history rewrites, unverified changes, or changes outside their feedback scope.
Do not require an engine or create lifecycle records unless explicitly selected.
Follow the shared [workspace policy](../homonto/references/workspace-policy.md)
and [autonomy policy](../homonto/references/autonomy.md).

**Success endpoint:** every selected PR has completed its continuation or has
an explicit failure or blocker, with a final per-PR report. Continue in the
same invocation unless the user names an earlier endpoint or asks to pause.

## Selection

Accept the same repository URL, optional `limit N` and `author USER`, or list
of PR references as `h-review-batch`. Follow its parse, preflight, enumeration,
pagination, deduplication, and batch-size consent rules. Resolve each canonical
PR and verify its source. Never silently truncate an all-open request or ask
again for an explicitly authorized count. Report the selected scope.

## Relationship and execution

Before implementing, ask whether the selected changes are independent or
connected unless the user already made that clear. For connected changes, ask
for missing dependency or ordering information when it cannot be established
from the PRs; distinguish a shared goal from an actual branch dependency.
Inspect base/head relationships and relevant cross-PR context rather than
assuming the user's answer proves a particular dependency.

For independent changes, handle each PR separately. For connected changes,
establish a dependency order, consider shared intent and interactions when
addressing feedback, and handle prerequisites before dependents. Refresh a
dependent's context and pinned refs after a prerequisite changes; do not merge,
rebase, copy commits across branches, or expand feedback scope merely because
the PRs are connected. If a prerequisite fails or the order is ambiguous, block
affected dependents and report why; unrelated PRs may proceed.

Run `h-continue-pr` to completion separately for each ready PR, including its
feedback trust decisions, safe checkout, verification, fresh remote identity,
normal push, addressed-thread resolution, and summary comment. Use isolated
workspaces or serialize writes; never switch a shared checkout while another
PR is being edited or verified. A failure on one independent PR must not erase
results or stop other ready PRs. Reconcile remote mutations before retrying.
Report each PR URL, outcome, pushed commit, verification, comment URL, remaining
feedback or blocker, and dependency impacts. Do not claim an unrun check passed.
