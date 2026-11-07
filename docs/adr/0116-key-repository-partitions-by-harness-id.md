# Key repository state partitions by harness id

- **Status:** Accepted
- **Date:** 2026-11-07

## Context

A declared repository's records live in its own `state.<repo>.json`
(ADR 0024). The adapters projecting into it are labelled `<harness>@<repo>`.
The OpenCode adapter recorded under `opencode`; the Claude Code adapter
recorded structured settings and links under `claude@<repo>` but copy-mode
subagents under `claude`. Readers disagreed too: snapshots used the harness
id, `status` checked both names, `explain` recognized only `opencode@`, and
apply-time provenance looked entries up under the label — so OpenCode repo
records never received an origin or last event.

## Decision

Inside every state partition, an adapter records under its harness id. The
`<harness>@<repo>` label stays the adapter's name in plans, status and
explain output; the partition file is what scopes a record to a repository.
One engine lookup (`Engine.Partition`) maps a label to its partition, key
and repository for every reader.

Loading a repository partition moves records and tombstones an older release
kept under the label to the harness id. The move persists with the next
apply. A key recorded under both names is ambiguous and fails the load
rather than choosing one.

## Consequences

- Plans, status and explain read the same records the adapters write;
  OpenCode repository records gain provenance from the first apply.
- A state file from an older release keeps working without manual steps;
  after one apply it no longer contains `claude@<repo>` keys. An older binary
  looks for Claude's repository records under the label and does not find
  them, so moving back across this change treats them as unrecorded.
- A partition left with a key under both names needs one record removed by
  hand before homonto loads it.
