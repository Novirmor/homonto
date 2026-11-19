# Verify squash PR recovery with GitHub

- **Status:** Accepted
- **Date:** 2026-11-19

## Context

A squash commit has no source parent, so local tree equality cannot establish
that a verified candidate was integrated. Existing PR receipts are external
claims, not evidence of a completed merge.

## Decision

For explicit sources in PR mode, accept a `squash-pr:` GitHub.com PR URL after
querying GitHub with `gh` and binding its merged PR, head, receiving repository,
branch and merge commit to the recorded source and local Git history. Require
the squash commit's entire tree to equal the PR head's tree and record the
commit ID alongside the URL. Do not accept an operator assertion or tree
equality alone as proof.

## Consequences

Recovery needs authenticated GitHub CLI access, the origin remote, and local
objects and receiving branch. It fails closed for GitHub Enterprise and other hosts, changed PR
heads, and squash results with additional base changes, even where those
merges may be legitimate. The older external-claim PR receipt is unchanged.
