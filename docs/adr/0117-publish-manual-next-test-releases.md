# Publish manual next test releases

- **Status:** Accepted
- **Date:** 2026-11-07

## Context

Testers need builds from `next` without tying their version numbers to a future
stable release or making unfinished builds the default installation.

## Decision

Publish manually requested builds of the captured `next` tip as GitHub
pre-releases named `homonto-next-N`, with immutable, independently numbered
tags. Run the complete automated gate and package all three binaries. These
test builds do not claim the stable release evidence or live verification.
The gate and build execute `next`'s code without a write token; only a
separate publish job, which runs nothing from `next`, creates the tag and
release. Stable releases retain their existing policy.

## Consequences

Testers can download checksummed archives without affecting stable latest.
The names are not Go module semantic versions and the interactive installer
does not accept them, so testers install archives manually. The tag must not be
moved or reused, even when publication fails after it was pushed.
