# Keep workflow records in versioned sidecars; JSON handoffs are metadata-only

- **Status:** Accepted
- **Date:** 2026-09-02

## Context

`onto handoff` is Markdown-only: prose a machine cannot replay, one aggregate
hash, no derived phase, no per-artifact digests. Verification evidence lives
as prose tables inside `verification.md`, which the binary cannot check.
Meanwhile `to handoff --json` exists but has no version envelope. Extending
`onto-state.yaml` instead is unsafe: an older binary silently drops unknown
same-version fields on save.

## Decision

We will keep machine-readable workflow records in versioned sidecars, not in
the state file: a handoff JSON envelope (schema-versioned, rejecting unknown
major versions) and `.onto/evidence.json` per change. Persisted handoffs carry
an explicit field allowlist — identity, phases, aliases, commits, gate IDs,
artifact hashes, next argv — and never artifact prose or free-form state,
which can carry secrets a user pasted into a plan. Interactive JSON may carry
the full state; `--write` persists the redacted view only. Evidence stores
hashes, never command argv or output; the agent runs commands itself, because
executing them through `onto` would ride the orchestrator's own allowlist.

## Consequences

- A fresh session can replay workflow state without parsing prose.
- Git remains the sole source of authorship and time (ADR 0021); sidecars
  store no identity.
- Older binaries ignore sidecars; newer ones treat their absence as legacy
  with a warning, so existing changes keep working.
- Two more files per change to keep atomic and symlink-safe.
