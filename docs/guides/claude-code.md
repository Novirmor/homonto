# Claude Code

homonto projects the same packages onto Claude Code as onto OpenCode, in
Claude Code's own formats ([ADR 0104](../adr/0104-project-claude-code-natively.md)).
Every builtin engine and hosting workflow runs there at the workflow tier,
with three accepted limitations ([ADR 0105](../adr/0105-accept-limited-claude-code-capabilities-per-guarantee.md)).
Verified against Claude Code 2.1.

## Turn it on

```toml
harnesses = ["opencode", "claude"]   # or ["claude"] alone

[engines.onto]
source = "builtin:onto"
scope = "project"

[routing.default.claude]
model = "sonnet"

[routing.engines.onto.stages.verify.claude]
model = "opus"
```

Every Claude-targeted agent needs a model in Claude Code's spelling, by pin
(`[subagents.<name>.claude]`) or policy (`[routing.<selector>.claude]`).
Claude Code cannot list its models, so an auto policy chooses among the
models `[routing.models.claude."<id>"]` describes. `homonto explain routing
--harness claude` shows each agent's model and why.

## What apply writes

| Where | What |
|---|---|
| `.claude/skills/`, `.claude/commands/`, `.claude/agents/` (or `~/.claude/…` for `scope = "user"`) | links to the materialized catalog, agents rendered for Claude Code |
| `~/.claude/settings.json` | your `[settings.claude]` keys |
| `.claude/settings.json` in the config root | homonto's hooks, the coordinator's runtime-tool permissions, `enabledMcpjsonServers` and `agent` (the session runs as the coordinator) |
| `.mcp.json` in the config root | the `homonto` server (`homonto runtime mcp`) |

homonto owns those project-settings keys. Put your own project rules in
`.claude/settings.local.json` or your user settings; Claude Code merges them.

## Trust the config root

Claude Code loads a project's hooks and starts its `.mcp.json` servers only in
a folder you trust. Start `claude` in the config root once and accept the
trust prompt; until then `claude mcp list` shows the `homonto` server as
pending. homonto never trusts a folder for you.

## How guarantees are kept

- **Agent limits.** What agent frontmatter can say is rendered there:
  `model`, `disallowedTools` (a read-only skeptic loses edit, shell and
  dispatch tools; every worker loses homonto's runtime tools) and `maxTurns`.
  Shell rules, record path denies and spawn lists ride in a `homonto_policy`
  value that homonto's `PreToolUse` hook enforces for the running agent.
- **Coordinator-only tools.** `mcp__homonto__*` tools are denied to any
  subagent and to a session running as another agent.
- **Context.** At startup, resume, `/clear` and after compaction the hook
  injects the continuation policy and the workflow snapshot. Mid-session,
  `homonto_status` reads it.
- **Publication.** The draft tools return `AskUserQuestion` arguments (at most
  four items). Only the answer to that exact question, from the
  coordinator's own call, approves an item; a question that arrives
  pre-answered is denied, and publication also asks through Claude Code's
  permission prompt.
- **Worker permission blocks.** A worker's prompt whose tool never ran before
  the worker stopped is handed back to the coordinator as refused.

`homonto doctor` prints the tier, each capability's level and every accepted
limitation. The generated `homonto/references/harness.md` tells the agents the
same.

## Not available

`AskUserQuestion` is absent in `claude -p`, so a workflow decision there is
reported pending rather than asked. There is no status panel.
`[plugins.claude]` and `[marketplaces.claude]` are rejected: manage Claude Code
plugins yourself.
