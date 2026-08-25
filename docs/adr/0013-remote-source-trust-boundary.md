# Establish a pinned, fail-closed remote-source trust boundary

- **Status:** Superseded by 0023
- **Date:** 2026-07-12
- **Change:** remote-source-trust

## Context

homonto resolved managed resources only from `builtin:` (compiled-in) and
`local:` (in-repo) sources — both fully trusted and offline. Roadmap item 10
requires accepting **remote** resources without reusing those local-source
assumptions against untrusted input. Fetching remote content exposes homonto to
redirects, path traversal, symlink escapes, archive bombs, tampered payloads,
compromised registries, and dependency substitution.

## Decision

We will add a `remote:` source type whose trust root is a **mandatory sha256
content pin** recorded in config and in `.homonto/remote.lock.json`. Resolution
runs a fixed **verify-before-mutate** pipeline — cache lookup → fetch → archive
validation → canonical digest → pin match → revocation — and materializes only
from a content-addressed cache. No target file (and no cache entry) is written
until every check passes, so malformed, oversized, tampered, or revoked content
fails closed before any mutation.

We will **not** auto-update: homonto never silently re-resolves a `remote:`
source to a different digest than its config pin. Advancing a pin is a manual
config edit. The digest is computed over a canonical, transport-independent tree
serialization so the same content pins identically across `https` tarballs and
`git` checkouts. Cache reclamation is an explicit, separate operation, so a
config revert can roll back from a warm cache offline.

The first increment's trust root is the content digest alone; a signing/PKI
provenance layer is deferred to item 11.

## Consequences

- A remote install is pinned, auditable (lockfile), reproducible, cacheable,
  offline-capable, revocable, and removable.
- The first pin is trust-on-first-use unless the operator obtains the digest out
  of band — an accepted, documented boundary until signing lands.
- `builtin:`/`local:` behavior is unchanged; remote is strictly additive and
  opt-in.
- Every attack class in the threat model maps to an enforced control and a
  fail-closed test (see `docs/guides/remote-source-trust.md`).
