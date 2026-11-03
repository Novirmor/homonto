# Own the Bitbucket Cloud transport

- **Status:** Accepted
- **Date:** 2026-11-02

## Context

GitHub and GitLab reach their APIs through first-party CLIs (`gh api`,
`glab api`). The coordinator's posture (ADR 0047) is host access only through
a host CLI, and every host command runs through the plugin's runner (ADR 0090).
Bitbucket Cloud has no CLI that mirrors `gh api`. Atlassian's Teamwork Graph
CLI (`twg bitbucket ...`) covers some pull request commands, but its support
for request-changes, drafts and a raw API passthrough is unverified, and it
authenticates Bitbucket separately from its OAuth flow. Bitbucket app passwords
stopped working on 2026-06-09. Scoped API tokens replace them, and they work
with Bearer auth.

The alternative was to hold credentials inside the runtime core and call REST
from it. That would give the core network access and a second credential path
that the coordinator's own `homonto host` calls could not share.

## Decision

homonto ships its own transport, `homonto host bitbucket api [--method M]
[--paginate] [--input -] ENDPOINT`, with the same shape as `gh api`, plus
`homonto host bitbucket auth status`. The Bitbucket provider and the
coordinator both call it as the Bitbucket host CLI.

- The token comes from `HOMONTO_BITBUCKET_TOKEN`, either the raw value or a
  `${pass:...}`/`${NAME}` reference resolved through homonto's secret
  resolver. It is sent as a Bearer token, or as Basic with
  `HOMONTO_BITBUCKET_EMAIL` when that is set.
- Only `https://api.bitbucket.org/2.0/` receives the token. Endpoints are
  relative paths that must stay under that root. Redirects are never
  followed. `--paginate` follows only same-origin `next` links and prints
  every page's `values` as one JSON array.
- The token, and its Basic encoding, are removed from every error. No output
  path prints it.
- Implementers already deny `homonto *`, so workers cannot reach the transport.

## Consequences

homonto now makes outbound HTTPS calls for one host and owns that client's
correctness: pagination, error shapes, and rate limits (1,000 requests/hour
per token). If Atlassian ships a CLI that covers the h processes, switching
means changing the provider's argv and this ADR. The runtime and doctrine do
not change.

The token lives in the environment of the process that runs OpenCode, or
behind `pass`. homonto stores no credential file of its own.
