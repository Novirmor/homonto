# Render external agents by intent

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

Only `builtin:` agents went through `internal/agentfm`. A `local:` or
`remote:` agent was linked verbatim, even when its file carried the same
`homonto:` intent block builtins use. So config validation rejected any
model override on it ("could never apply"). External agents could not get
per-config models, rendered permissions, service shell rules or
harness-native output (ADR 0082).

## Decision

Rendering is decided by content, not by source kind. A `local:` or `remote:`
agent whose file has a `homonto:` block renders like a builtin: model,
variant and steps come from `[subagents.<name>.opencode]` (and are
required), permissions come from the intent, and service rules come from
installed services. The engine renders these agents before planning and
writes them to `.homonto/rendered/subagents/<name>.opencode.md`, which the
adapter links (or copies). A remote agent renders once apply has fetched
and verified it.

- A file without a block stays verbatim. A model override on it fails, with
  the fix.
- Malformed intent fails. It is never projected as a weakened verbatim agent.
- An intent agent may not declare `model`, `variant`, `steps` or `permission`
  itself. The render owns those keys. This applies to builtins too.
- External agents render under the identity `external/<name>`, which never
  matches a builtin catalog name. Their frontmatter therefore cannot claim a
  builtin role's workflow tools or the workspace directory grants the engine
  computes for bundled agents (ADR 0068), whatever name it uses.

## Consequences

A user's own agent can be tuned per config and gets the same permission
guarantees as a shipped one. An existing external agent that already had a
`homonto:` block, and relied on being linked raw, now needs a model block
and must drop any `model:` or `permission:` it declared. Load can no longer
reject every override on a non-builtin source, because the content decides.
The check moves to the engine, which runs before every command that plans,
so the error arrives at `plan`, not at parse time.
