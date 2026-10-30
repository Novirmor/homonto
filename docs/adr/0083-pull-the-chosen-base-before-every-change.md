# Pull the chosen base before every change

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

`onto new` and `to new` freeze each source's base from the local branch, by
default the checkout's current HEAD. Nothing fetched first, so a stale local
`main` produced a stale base, and the drift surfaced only as conflicts at PR
time. Users asked that every change start from the latest remote state, and
that the agent ask which branch to start from instead of assuming the current
one.

The frozen base must stay a local branch: worktree allocation, evidence, and
close all check that `base_ref` is reachable from `refs/heads/<base_branch>`
([ADR 0036](0036-separate-the-diff-base-from-the-integration-branch.md)).
Accepting `origin/main` as a base would have meant loosening each of those
checks.

## Decision

We will update the local branch instead of accepting remote refs as bases.
`homonto workspace pull --repo <alias> --branch <branch>` fetches the branch's
upstream and fast-forwards only that branch. A branch checked out somewhere is
updated in that checkout only when it has no uncommitted tracked changes. A
diverged branch, a missing upstream, or a failed fetch is refused; the command
never merges, rebases, resets, or stashes.

The workflow skills ask once which branch each selected repo starts from
(recommending the remote default branch), pull it, then pass it explicitly as
`--base <alias>=<branch>` on `new`. A pull refusal is a blocker the user
resolves; it is not routed around. Resuming a change never pulls.

## Consequences

Every new change needs network access to the remote, and an offline start now
asks the user instead of silently using the local branch. Pulling a clean
checked-out base branch moves that checkout, which is what the user asked
for; a dirty one blocks until the user decides. The `--base` default in the
binaries is unchanged, so a direct `onto new` without the skills still freezes
the current HEAD: the pull is enforced by the skills, not the binary. The `no`
engine's `workspace task create` has no base selection and is not covered yet.
