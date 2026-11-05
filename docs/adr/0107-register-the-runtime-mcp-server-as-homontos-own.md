# Register the runtime MCP server as homonto's own

- **Status:** Accepted
- **Date:** 2026-11-05
- **Amends:** [0085](0085-bring-your-own-mcp-servers-and-skills.md) (what
  "homonto manages only what it ships" covers on MCP)

## Context

ADR 0085 made MCP servers bring-your-own and removed `[mcps.*]`. ADR 0103
then made the runtime core an MCP server that Claude Code launches
(`homonto runtime mcp`), and ADR 0104 has the adapter write it into
`.mcp.json`, the same file that holds the user's own project servers. Read
together, the two looked like managed MCP coming back, and the harness
design document still described shared `[mcps]` declarations as an open
question.

## Decision

The runtime MCP server is a resource homonto ships, like its agents and
hooks. It is not a user MCP declaration, and `[mcps.*]` stays rejected.

- **Identity.** One server, named `homonto`, of type `stdio`, command
  `homonto` (resolved on `PATH`), args `runtime mcp --harness claude --config
  <selected config> [--host <id>...]`. Nothing in `homonto.toml` changes its
  name or command. It carries no `env` and no secrets: credentials stay with
  the host CLIs the core runs.
- **Ownership.** homonto owns exactly the key `mcpServers.homonto` in the
  config root's `.mcp.json`, through the same managed-key projection as every
  other native file (ADR 0014). Other servers in the file are never read,
  listed, rewritten or pruned. A user server already named `homonto` is
  replaced, and plan shows that as an update. The name is reserved.
- **Lifecycle.** The entry exists while Claude Code is targeted and the
  workflow runtime is enabled. Apply creates it, and apply removes it when
  either condition stops holding. Claude Code starts and stops the process.
  Enabling it is the folder-trust step: homonto writes
  `enabledMcpjsonServers = ["homonto"]` and never trusts a folder for the
  user.
- **Drift and reporting.** Status reports drift on the entry like any managed
  key. `homonto doctor` reports whether the entry is present and matches the
  selected config, and whether `homonto` is on `PATH`, and it never reports
  the user's servers.
- **OpenCode has no MCP registration.** The OpenCode plugin shim starts
  `homonto runtime serve` itself (ADR 0103), so homonto writes nothing to the
  `mcp` key of `opencode.jsonc`.
- **Per harness.** Each later harness registers this one server through its
  own native MCP location, under the same rules: one fixed name, homonto's
  key only, no user-server enumeration.

## Consequences

`.mcp.json` becomes a shared file: the user edits their servers, homonto
edits its own, and neither overwrites the other. The reserved name is the
cost. A user who ran their own server as `homonto` must rename it.

A bare `homonto` command depends on `PATH` in the environment Claude Code
starts it from. A missing or older binary fails the tools closed
(`[runtime_unavailable]`, `[runtime_mismatch]`), and doctor names the gap.
Pinning an absolute binary path in the file would break every reinstall and
version manager switch.

Injecting the user's own MCP servers into homonto's agent policies, the
follow-up ADR 0085 owed, is still open. This decision does not address it.
