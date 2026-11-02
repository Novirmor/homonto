# Name hosting workflows per host, with shared process names

- **Status:** Accepted
- **Date:** 2026-11-02

## Context

The GitHub workflow was the `h` package. GitLab.com and Bitbucket Cloud
variants are coming (roadmap M1), and they run the same six processes: spike,
resolve issue, review, continue, resolve conflicts and batch review. A
workspace can declare repositories on more than one host, so one config may
need two hosting workflows at once. Users and doctrine already know the
commands as `/h-*`.

## Decision

The GitHub package is renamed to `h-workflow-gh`, and the hosts that follow
are `h-workflow-gl` and `h-workflow-bb`.

`[workflows.h]` and `source = "builtin:h"` fail at load, and the error names
`[workflows.h-workflow-gh]` `source = "builtin:h-workflow-gh"`. Following
ADR 0067 and ADR 0081, there is no alias and no migration shim.

Every host keeps the same process names: `/h-spike-issue`,
`/h-resolve-issue`, `/h-review-pr`, `/h-continue-pr`, `/h-resolve-conflicts`
and `/h-review-batch`. The process skills and commands are host-neutral and
identical in every hosting package, so co-installing packages deduplicates
them the way the shared `homonto` skill is deduplicated across engines
(ADR 0042). Each package adds only its host-specific parts: transport,
publication reference and permission rules. The target repository's origin
selects the host.

Hosting workflows may be installed together in one config.

## Consequences

Existing configs must be rewritten by hand before they load. The commands
users type do not change, and neither does the GitHub process.

A shared process skill must not take on host-specific behavior. Host
differences go in the host package, or in the documented difference map
(M1.3), never in a per-host fork of a process. "PR" stays the process
vocabulary on GitLab, where the object is a merge request. `/h-review-pr`
reviewing an MR is a naming cost we accept so that every host has one
command set.
