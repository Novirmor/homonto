# Release integrated workspace task branches

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

`workspace task cleanup` removed a task-created worktree but kept its branch,
so every finished `no` task left a `work-*` branch in the user's repository.
The branch was named `work-<name>-<alias>`. Task names and repository aliases
may both contain `-`, so two different tasks could need the same branch.

## Decision

Task branches are `work-<name>/<alias>`. Neither part may contain `/`, so each
(name, alias) pair has its own branch.

After cleanup removes a task-created worktree, it deletes that worktree's
branch. Cleanup deletes the branch only while it still points at a commit
already integrated into the recorded target, so no commit becomes unreachable.
A retried cleanup finishes this step too.

A branch that already exists when `create` runs is never adopted or deleted,
even one an interrupted `create` left behind. Homonto cannot tell it apart
from the user's own branch, so the user deletes it and reruns `create`.

## Consequences

Cleanup leaves no task branches behind; integrated commits stay reachable from
the target branch. Allocated tasks keep the branch they recorded. An
incomplete allocation recorded under the old name cannot resume; abandon it
with cleanup. A crash between worktree removal and branch deletion is recovered by the next
cleanup, and only while the branch is still integrated.
