# Harness capabilities

A package lists the harness features it needs in `[requires].harness`. Each
adapter declares what its harness provides at a pinned version, `enforced` or
`instruction-only`. `homonto plan` fails before projecting anything when a
target harness does not enforce a required capability, and names the package,
each gap and the harness version
([ADR 0097](../adr/0097-require-harness-capabilities-by-name.md)).

| Capability | Meaning |
|---|---|
| `skills` | Loads `SKILL.md` directories |
| `commands` | User-invocable slash commands that route to an agent |
| `subagents` | Defined agents the coordinator can dispatch, with fresh context |
| `agent-tool-limits` | Per-agent tool and permission limits the harness enforces |
| `shell-rules` | Per-command shell allow/ask/deny the harness enforces |
| `question-dialog` | A structured native tool for asking the user |
| `context-hooks` | Inject context at session start and at compaction |
| `permission-events` | Observe asked/answered/rejected permission prompts, per session |
| `mcp` | Runs configured MCP servers |
| `status-ui` | A surface for workflow status |

`instruction-only` never satisfies a requirement. Plan reports it as
`<capability> (instruction-only only)` so the gap is not mistaken for a
missing feature.

This is not `[dependencies].capabilities`. Those are interfaces one package
provides another (`name@major`), resolved between packages.

| Harness | Version | Declares |
|---|---|---|
| OpenCode | 2 | every capability, enforced |
