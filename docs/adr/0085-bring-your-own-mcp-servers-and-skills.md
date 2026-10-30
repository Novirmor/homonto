# Bring your own MCP servers and skills

- **Status:** Accepted
- **Date:** 2026-10-29
- **Partially supersedes:** [0024](0024-multi-repo-designated-state-cross-repo-effect.md)
  (per-repo MCP servers and `local:` skills and commands)

## Context

homonto projected `[mcps.*]` servers into `opencode.jsonc` and symlinked
`local:` skills and commands from `homonto/` into OpenCode. Both duplicated
what OpenCode already does well: users configure MCP servers and drop skill
directories in place themselves. Owning them meant keeping a projection,
drift detection, pruning and repo targeting correct for content homonto adds
nothing to. The product is the workflow homonto ships, not the user's own
tooling.

## Decision

We will make MCP servers and the user's own skills and commands
bring-your-own. homonto manages only what it ships: workflow packages,
builtin skills and commands, agents, plugins and settings.

- `[mcps.*]`, an `mcp` key under `[settings.opencode]`, and a `local:` source
  on `[skills.*]` or `[commands.*]` fail at load, naming the fix. `local:`
  stays for subagents and packages.
- No migration tool. On the first apply, entries an earlier release projected
  are **released**: their state is dropped and the files stay byte for byte.
  MCP entries remain in `opencode.jsonc`; links to the user's own
  `homonto/skills/` and `homonto/commands/` content remain where they are.
  `plan` marks each as `>` (released), not `-`.

We rejected keeping `[mcps.*]` as an optional convenience: a half-managed
surface still needs every projection guarantee, and two places to declare a
server invite drift between them.

## Consequences

homonto's config and adapter shrink, and users edit MCP servers with
OpenCode's own syntax, including `{env:VAR}` secrets that never land in the
file. Secret references in `homonto.toml` remain for settings.

The cost is that homonto no longer knows what the user brought. Its agents
render a deny baseline for directories outside the project, so a skill in
`~/.config/opencode/skills/` is not readable by homonto's workers, and
MCP tools are not covered by the per-agent permissions at all: a read-only
worker inherits the user's global MCP permissions. Injecting information about
the user's own MCP servers and skills into homonto's agents, with explicit
permissions, is the follow-up this decision owes.
