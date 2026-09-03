# Separate the diff base from the integration branch

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

onto records `base_ref` as `git rev-parse HEAD` so verification compares against
an immutable commit. Close then used that value as a `git checkout` target and
as `gh pr create --base`. Local integration could detach HEAD, and GitHub expects
a branch name. Worktree isolation also prevents the change worktree from
checking out a base branch already checked out elsewhere.

## Decision

State will record `base_branch` separately. `base_ref` remains the immutable
diff and verification anchor. Close targets `base_branch` for local merge and
pull requests. A worktree-isolated change merges from the existing clean
worktree that has the base branch checked out.

## Consequences

Close no longer treats a commit SHA as a branch. New changes record both values;
legacy changes derive and record the target branch before archival. State gains
one optional field and one setter, and detached-HEAD changes need repository
policy to identify their target branch. Both anchors are set-once; close
requires them, rejects an invalid branch name, and resolves `base_ref` to a
canonical commit at set time. One recorded base branch applies across the
change's whole repository scope — the config repository and every selected
sibling must share it.
