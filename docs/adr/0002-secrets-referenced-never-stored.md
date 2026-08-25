# Reference secrets by token; never store or print plaintext

- **Status:** Superseded by 0023
- **Date:** 2026-07-03
- **Change:** homonto-v1-core

## Context

MCP servers need API keys. `homonto.toml` is meant to be committed and
shared; tool config files and homonto's own state must not become secret
stores, and `plan` output must be safe to paste anywhere.

## Decision

We will keep secret values outside the repo and reference them as tokens:
`${pass:PATH}` (resolved via `pass`) and `${ENV_VAR}` (environment
fallback). `plan` never resolves or prints a secret — it shows the token.
`apply` resolves only after confirmation, and all resolutions needed for a
write must succeed before any file is written. `.homonto/state.json` stores
the unresolved token plus a sha256 hash of the applied value — never
plaintext.

## Consequences

- Config and state are safe to share; drift on secret-backed values is
  still detectable via the hash, and repeat applies stay no-op.
- A missing reference aborts the whole apply before touching anything.
- The resolved value does land in the target tool's file (the tool needs
  it) — homonto's guarantee covers its own artifacts and output.
