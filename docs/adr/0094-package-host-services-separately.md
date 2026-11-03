# Package host services separately

- **Status:** Accepted
- **Date:** 2026-11-03
- **Extends:** [0047](0047-deny-open-web-and-compound-shell-around-github-intake.md) to every host

## Context

GitHub, GitLab and Bitbucket support arrived inside the hosting workflows.
The runtime enabled a host's draft tools when it saw `builtin:h-workflow-gh`,
`-gl` or `-bb`, from a table hard-coded in the config package. A custom
workflow that wanted GitHub drafts had to install `h-workflow-gh` and all of
its process skills. ADR 0047's trust posture (consent checkpoints for
destructive and arbitrary-API host commands) was written for GitHub. GitLab
got the same asks, but Bitbucket's transport, `homonto host bitbucket api`,
could DELETE without a prompt.

## Decision

- `service` is the third package kind, configured under `[services.<name>]`.
  A service declares an `id`, its operations (`read`, `draft`, `publish`), its
  transport, its credentials (`cli` login checks or `env` variable names), an
  optional built-in runtime provider, and shell rules for rendered agents.
- A workflow or engine requires services by id, with a default package:
  `[requires.services] github = "host-gh"`. The config's own service for that
  id wins. Otherwise the default is installed beside the requirer. Two
  packages implementing one id fail at load.
- The draft runtime enables providers from installed services, not from
  workflow names. `host-gh`, `host-gl` and `host-bb` package the three hosts
  from M1, and each `h-workflow-*` requires its host by default, so existing
  configs behave the same.
- Trust posture: contributed rules only tighten. Primaries gain asks and
  denies, other shell-capable agents gain denies, and nothing grants an
  allow. A remote or local service therefore cannot widen any agent.
- Builtin exception: the builtin services contribute no rules. Host-CLI asks
  and implementer denies stay in the agent baselines, because engines deliver
  through those CLIs with no service installed. The baseline now also asks
  before any Bitbucket API DELETE, which extends ADR 0047's checkpoint to the
  third host.

We rejected keeping service dependencies as `[dependencies].frameworks`.
That would install a fixed package, and a user could not replace it with
their own implementation of the same service.

## Consequences

A custom workflow can use one host's operations and draft tools without any
hosting workflow, and a fork of a host service is a one-table change. Load
now reads every local package's manifest, so a missing local root fails at
load instead of at plan. The cost is two places that describe host rules,
baselines and service manifests. A host added later must put its rules in a
service, not in the baselines. The baselines stay limited to the hosts
engines deliver to.
