# Harness capabilities

A package lists the harness features it needs in `[requires].harness`. Each
adapter declares what its harness provides at a pinned version: `enforced`,
`limited` (provided, with a gap the limited-mode policy accepted,
[ADR 0105](../adr/0105-accept-limited-claude-code-capabilities-per-guarantee.md)),
or `instruction-only`. `homonto plan` fails before projecting anything when a
target harness does not provide a required capability, and names the package,
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

`enforced` and `limited` satisfy a requirement; `instruction-only` never does. Plan reports it as
`<capability> (instruction-only only)` so the gap is not mistaken for a
missing feature.

This is not `[dependencies].capabilities`. Those are interfaces one package
provides another (`name@major`), resolved between packages.

| Harness | Version | Tier | Declares |
|---|---|---|---|
| OpenCode | 2 | workflow | every capability, enforced |
| Claude Code | 2.1 | workflow | `skills`, `commands`, `subagents`, `agent-tool-limits`, `context-hooks`, `mcp` enforced; `shell-rules`, `question-dialog`, `permission-events` limited; no `status-ui` |

The tier is derived from the declaration (ADR 0082): **workflow** needs
`skills`, `commands`, `subagents`, `agent-tool-limits`, `shell-rules`,
`question-dialog`, `context-hooks` and `mcp`; **lite** needs `skills`,
`subagents`, `question-dialog` and `mcp`; anything less is **projection**.
`homonto doctor` reports each targeted harness's tier, every capability's
level, and each accepted limitation.

Doctrine names capabilities ("the native question dialog"), never a
harness's tools. Apply generates `homonto/references/harness.md`, mapping
each capability to its native form on every targeted harness; a catalog test
fails on harness tool names anywhere else.
