# Clean up at close

- **Status:** Accepted
- **Date:** 2026-10-30
- **Partially supersedes:** [0075](0075-own-terminal-cleanup-in-the-binaries.md)
  (no automatic deletion at completion; integration required for removal) and
  [0048](0048-one-declared-workspace-tmp-directory.md) (homonto never deletes
  tmp content)

## Context

Closed changes left worktrees and scratch behind. ADR 0075 made cleanup an
explicit, approved step and removed only worktrees whose commits were already
in the local base branch. Most changes close by opening a PR, so at close the
commits are on the change branch only: cleanup refused until the PR merged and
someone pulled, and then waited for a separate approval. ADR 0048 left every
tmp file for a human to delete, and tmp had no per-change layout to clean by.

## Decision

We will clean up at close.

- Closing a change authorizes its cleanup. The coordinator runs
  `<engine> cleanup apply` at close without asking, and its permission for that
  command is allowed instead of a prompt. Engine-free task cleanup and h
  continuation checkouts follow the same rule.
- A terminal, clean, owned worktree may be removed when its HEAD is integrated
  into the base or contained by its retained branch. Branches are never deleted
  by worktree removal, so a PR delivery not yet merged loses nothing. Task
  cleanup keeps an unmerged task branch instead of releasing it.
- A change keeps its scratch under `<tmp>/<engine>/<change>/`. Cleanup deletes
  that folder with the worktrees. `homonto tmp clean` previews, and with
  `--yes` removes, all scratch that belongs to no active or interrupted change.
  `apply` still never deletes tmp content.

All other ADR 0075 guarantees stay: the binaries own eligibility, apply is
generation-bound, dirt and ownership checks are not bypassable, and archives
and branches remain.

## Consequences

Closed work stops accumulating checkouts and scratch, and cleanup needs no
second confirmation. The cost is that a worktree disappears while its PR may
still be in review: follow-up fixes check out the retained branch again rather
than reusing the old directory. `tmp clean` deletes scratch of other workflows
(h, no) that are not tracked as changes, so it previews by default. Scratch
written outside `<tmp>/<engine>/<change>/` is only removed by `tmp clean`.
