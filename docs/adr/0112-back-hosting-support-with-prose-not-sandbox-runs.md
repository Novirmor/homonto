# Back hosting support with prose, not sandbox runs

- **Status:** Accepted
- **Date:** 2026-11-06

## Context

M1 (#30) asked each hosting workflow (`h-workflow-gh`, `h-workflow-gl`,
`h-workflow-bb`) to demonstrate its processes on real sandbox repositories,
and the 1.0 gate (ADR 0110, `internal/starter`) required a `sandbox/<host>`
evidence key per host cell. That needs throwaway accounts, tokens and
fixtures on GitHub, GitLab.com and Bitbucket Cloud, and it held the 1.0 tag
on work that no maintainer was going to keep running. The code it would test
already has offline coverage: fake-provider tests for every host in
`internal/hostruntime` and `internal/bitbucket`, which run in `gate/full`.

## Decision

We will back hosting support with prose: the
[hosting differences map](../guides/hosting-differences.md) and the hosting
guides, plus the offline provider tests. No release requires a live sandbox
run, and hosts add no evidence key to a cell. A gate key, `docs/hosting`,
requires the release notes to link the differences map and repeat its
**(unverified)** claims as unverified. `scripts/hosting-sandbox.sh` stays as
an optional manual tool whose runs may correct the map but are never
release evidence.

## Consequences

The 1.0 gate no longer waits on host accounts. The cost is that homonto
never proves a draft, approval or publish against a real host before a
release: a provider API change or a wrong **(unverified)** claim surfaces
only when a user hits it. Release notes must say so plainly instead of
claiming verified hosting. M1.8 (#43) is closed as not planned.
