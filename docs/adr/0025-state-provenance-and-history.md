# Record resource provenance and last-change in state

- **Status:** Accepted
- **Date:** 2026-09-02

## Context

`homonto plan` shows what changes, but not why a managed key exists: who
declared it (config table, framework, transitive dependency), when it last
changed, or what removing it would do. State records only the latest desired
value and applied hash; a deleted key's record disappears entirely. Answering
"why is this here" today means re-deriving expansion by hand.

## Decision

We will extend `state.json` (schema 3) with per-resource provenance: origins
(direct declaration, framework owner, catalog provider, source class, scope,
repo), one last-event record per live resource (operation ID, action, cause,
time), and a bounded ring of the latest 100 removal tombstones. Link entries
additionally record semantic intent (kind, resource, scope, source class,
normalized target) instead of being only a raw `dst -> src` string. One schema
owns all of it; there is exactly one migration from schema 2.

## Consequences

- Legacy entries load with unknown provenance rather than guessed facts.
- A no-op apply leaves history fields untouched.
- State grows by one record per resource; tombstones are bounded, so the file
  stays small.
- `homonto explain` can name origin, destination, partition, last change, and
  removal cause without re-deriving expansion — at the cost of maintaining the
  provenance through every adapter write path.
