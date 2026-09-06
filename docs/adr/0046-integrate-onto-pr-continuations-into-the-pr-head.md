# Integrate onto PR continuations into the PR head

- **Status:** Accepted
- **Date:** 2026-09-06

## Context

`h-continue-pr` checks out a pull request head, but onto builds on a new change
branch. Treating the existing pull request as onto's `pr` integration left the
verified commits off the pull request and could open a second pull request.

## Decision

`h-continue-pr` will make the PR head the onto change's `base_branch`, close
with `integration: merge`, then merge the verified change branch into that PR
head. It records the real merge commit through `onto complete-integration` and
pushes the PR head.

## Consequences

The existing PR receives one merge commit containing the archived change and
the verified work. The coordinator must retain or rebuild the PR head details
before resuming integration. We reject a new `existing-pr` integration mode
because the current merge receipt already proves the needed local history.
