# Project Claude Code natively

- **Status:** Accepted
- **Date:** 2026-11-04
- **Design:** [`docs/harness-architecture.md`](../harness-architecture.md)

## Context

M4.4 returns Claude Code as a second adapter (ADR 0082). The harness
architecture left four questions open: how a harness is enabled and how shared
declarations target it, how scope maps to each harness, version pinning, and
how per-agent limits are rendered where the harness has no per-agent shell
rules.

## Decision

- **Enabling and targeting.** A top-level `harnesses = ["opencode", "claude"]`
  names the harnesses a declaration without its own `targets` goes to; absent
  means OpenCode alone, so existing configs are unchanged. An explicit
  `targets` still wins. Anything Claude-specific is native: `[settings.claude]`
  goes into `~/.claude/settings.json`, `[subagents.<name>.claude]` and
  `[routing.<selector>.claude]` take Claude Code model aliases or ids.
- **Scope.** `scope = "project"` maps to `<root>/.claude/`, `scope = "user"` to
  `~/.claude/`, as OpenCode's map to `.opencode/` and `~/.config/opencode/`.
- **One harness map, per-harness sections.** Skills are one materialized
  catalog linked into every harness, so the generated
  `homonto/references/harness.md` carries a section per targeted harness
  rather than a separate catalog per harness.
- **The project files carry the integration.** homonto owns these keys in the
  config root's `.claude/settings.json`: `hooks.<event>` for the events it
  uses, `permissions.allow|ask|deny`, `enabledMcpjsonServers` and `agent` (the
  coordinator is the session's agent). `.mcp.json` carries the `homonto`
  server. Personal rules belong in `.claude/settings.local.json` or user
  settings, which Claude Code merges.
- **Agent policies ride in the agent file.** What agent frontmatter can say is
  rendered there (`model`, `disallowedTools`, `maxTurns`). Shell rules, path
  denies and the spawn list — which Claude Code cannot scope to one agent —
  render as a `homonto_policy` frontmatter value. One `PreToolUse` hook
  (`homonto runtime hook`) evaluates the running agent's policy from the
  `agent_type` Claude Code reports, with the same last-match-wins rules
  OpenCode applies. The intent is never weakened: an agent that declares a
  variant or effort fails the render.
- **Version pinning.** The declaration is pinned to Claude Code 2.1, the
  version its event shapes were verified against (2.1.318). A newer harness is
  not detected or refused; the end-to-end matrix is what keeps the tier honest
  (ADR 0082, "stale support is worse than none").

## Consequences

A config that adds `claude` to `harnesses` must give every Claude-targeted
agent a Claude model, by pin or `[routing.*.claude]` policy, or plan fails.
homonto overwrites the project-settings keys it owns, visibly in plan. Every
tool call in a Claude Code session runs one short `homonto` process for the
policy hook. Main-thread enforcement depends on the session running as the
coordinator agent; a plain `claude` session in another directory carries no
homonto policy. `[plugins.claude]` and `[marketplaces.claude]` stay rejected:
Claude Code plugins are bring-your-own.
