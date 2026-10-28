# Harness architecture — design document

Status: **architecture only, not implemented.** The decision is
[ADR 0082](adr/0082-support-harnesses-natively-in-tiers.md). This document
describes how homonto supports more than one agent harness: OpenCode today,
then Claude Code, Codex, and Google Antigravity (`agy`). Where it says
"verify", the capability has not been checked against a pinned harness
version and must be before that harness is assigned a tier.

## Principles

1. **Native in, native out.** A harness is configured in its own vocabulary
   and rendered in its own formats. There is no neutral config schema and no
   translation of one harness's settings into another's.
2. **Capabilities, not names.** Engines declare what they need; adapters
   declare what a pinned harness version provides. Support follows from the
   match.
3. **Fail closed.** A declaration a harness cannot render, or an engine whose
   needs a harness cannot meet, is a plan or selection error that names the
   gap. Nothing is dropped or approximated silently.
4. **One runtime, thin shims.** Workflow runtime logic lives once in the Go
   binary. A harness integration carries only what that harness needs to reach
   it.
5. **Stale support is worse than none.** The v0.13.0 removal happened because
   Claude Code and Codex projections outlived the workflows they no longer ran.
   A harness keeps its tier only while its capability fixtures and end-to-end
   checks pass.

## Layers

```text
homonto.toml (per-harness native tables)
      │
      ▼
engine ──▶ registry ──▶ harness adapter ──────────────▶ native files
              │          ├─ capability declaration      (config, agents,
              │          ├─ renderers: agents, commands,  commands, hooks,
              │          │   permissions, hooks/plugins   plugins)
              │          └─ runtime shim
              ▼
       homonto runtime core (status, handoff, GitHub drafts,
       permission handback) ── exposed to every harness over MCP
```

### Configuration

Each harness owns its tables. Today's OpenCode keys (`[settings.opencode]`,
`[tui.opencode]`, `[plugins.opencode.*]`, `[integrations.opencode]`,
`[subagents.<agent>.opencode]`) are the pattern: a harness `<h>` gets
`[settings.<h>]`, `[subagents.<agent>.<h>]` for its model route, and so on,
holding values in that harness's own spelling. homonto validates them against
that harness's schema and never maps them across harnesses.

Content that is the same file on every harness is not configuration and is not
translated: a skill is a `SKILL.md` directory on all four harnesses, so the
adapter links or copies it to the harness's skill location as-is. How shared
declarations such as `[mcps]`, `[skills]`, and `[commands]` select their
target harnesses is an open question below.

### Harness adapter

One package per harness, registered in `internal/adapter/registry`. It
implements the existing `Adapter` and `Describer` interfaces on the shared
projection control flow (ADR 0014), and adds:

- **A capability declaration** for a pinned harness version (next section).
- **Renderers** from homonto's catalog sources to native files. The catalog's
  agent sources keep their `homonto:` intent block (read-only, shell rules,
  network, dialogs, spawn list), and each adapter renders it to that harness's
  agent format, as `internal/agentfm` does for OpenCode today. Commands, hooks,
  and plugins render the same way. An intent a harness cannot express fails the
  render. It is not weakened.
- **A runtime shim** (see Runtime).
- **Conformance and fixtures.** The shared conformance suite, plus pinned
  real-format fixtures for each native file and event shape, in the same way the
  OpenCode permission events are pinned today.

### Capability declaration

Each adapter declares, per pinned version, what the harness provides:

| Capability | Meaning |
|---|---|
| `skills` | Loads `SKILL.md` directories |
| `commands` | User-invocable slash commands that route to an agent |
| `subagents` | Defined agents the coordinator can dispatch, with fresh context |
| `agent-tool-limits` | Per-agent tool/permission limits the harness enforces (not just instructions) |
| `shell-rules` | Per-command shell allow/ask/deny the harness enforces |
| `question-dialog` | A structured native tool for asking the user |
| `context-hooks` | Inject context at session start and at compaction |
| `permission-events` | Observe asked/answered/rejected permission prompts, per session |
| `mcp` | Runs configured MCP servers |
| `status-ui` | A surface for workflow status (optional everywhere) |

### Tiers

An engine lists the capabilities it requires; a harness's tier is the
highest tier whose requirements it meets.

| Tier | Runs | Requires |
|---|---|---|
| **Workflow** | onto (full and light), to, no, h | `skills`, `commands`, `subagents`, `agent-tool-limits`, `shell-rules`, `question-dialog`, `context-hooks`, `mcp` |
| **Lite** | to, no, and h review-only skills | `skills`, `subagents`, `question-dialog`, `mcp`; worker limits may be instruction-only and are reported as such |
| **Projection** | No workflows; config, MCP, skills, and agents projected | The resources it renders |

Full onto's guarantees (read-only skeptics and reviewers, coordinator-only
publication and state) depend on `agent-tool-limits` and `shell-rules`, which
is why they gate the workflow tier. A harness that enforces those guarantees
only through instructions cannot honestly run full onto. `homonto doctor`
reports each harness's tier and which guarantees are enforced or
instruction-only.

## Known harness capabilities

Starting points from current documentation, to be verified against pinned
versions before any tier is assigned.

| Capability | OpenCode 2 | Claude Code | Codex | Antigravity |
|---|---|---|---|---|
| Native config | `opencode.jsonc`, `cli.json` | `settings.json`, `.mcp.json` | `config.toml` | `mcp_config.json`, rules files (paths vary by source) |
| `skills` | yes | yes | yes (`SKILL.md`) | yes (`.agents/skills/`) |
| `commands` | yes | yes | verify | verify (workflows) |
| `subagents` | yes (Markdown) | yes (`.claude/agents/`) | yes (TOML in `.codex/agents/`) | yes (`.agents/agents/`; built-in `research`, `self`, `custom`) |
| `agent-tool-limits` | yes (per-agent `permission`) | yes (per-agent tools, settings permissions) | coarse (per-agent sandbox policy) — verify | verify (`research` is read-only) |
| `shell-rules` | yes (allow/ask/deny patterns) | yes (allow/ask/deny rules) | verify (approval policy) | verify |
| `question-dialog` | yes (`question`) | yes (`AskUserQuestion`) | verify | verify |
| `context-hooks` | yes (plugin hooks) | yes (`SessionStart`, `PreCompact`) | yes (`SessionStart`, `PreCompact`) | verify (plugins bundle hooks) |
| `permission-events` | yes (plugin events) | yes (`PermissionRequest` hook) | yes (`PermissionRequest` hook) | verify |
| `mcp` | yes | yes | yes | yes |
| Expected tier | Workflow (today) | Workflow | Lite, Workflow if limits hold | Projection, then reassess |

## Runtime

Today the workflow runtime is the OpenCode TypeScript plugin
(`catalog/plugins/homonto-workflow`): coordinator-only `homonto_status` and
`homonto_handoff` tools, GitHub draft/approve/publish tools, context injection
at request and compaction, the status panel, and the worker permission
handback (ADR 0077).

Target shape:

- **Runtime core in Go**, served by the `homonto` binary as an MCP server. It
  owns the read-only status and handoff tools, the GitHub draft lifecycle, and
  handback state. Every harness runs MCP, so this part is written once.
- **Per-harness shim** for what MCP cannot do: injecting the continuation
  policy and recovery snapshot at session start and compaction, turning the
  harness's permission events into handback notices, enforcing coordinator-only
  tool access where the harness can, and any status UI. On OpenCode the shim is
  a slimmer version of today's plugin. On Claude Code and Codex it is hooks
  configuration plus small hook commands that call the binary.
- **Approval stays native.** Publication approval uses the harness's
  `question-dialog`; the core never invents its own approval channel.

## Doctrine

The catalog's skills and agents currently name OpenCode's tools directly. To
run on a second workflow-tier harness, they name capabilities instead ("ask
through the harness's native question dialog", "dispatch the worker"). Apply
generates a per-harness `references/harness.md`, like the generated
`workspace.md`, mapping each capability to that harness's actual tool and
saying which guarantees are enforced there. Engine selection reads the tier,
refuses an engine the harness cannot run, and says why.

## Lessons from v0.13.0

- Projection support is cheap; workflow support is the cost. A harness ships at
  the tier its runtime and doctrine actually reach, never at the tier its
  config projection suggests.
- Each tier needs a live check: capability fixtures in unit tests and, for the
  workflow tier, a real-tool run in the end-to-end matrix. A harness whose
  checks are not maintained drops a tier, not into silent staleness.

## Implementation order (when implementation starts)

1. Extract the runtime core from the OpenCode plugin into the binary behind MCP,
   keeping OpenCode behavior identical. This proves the core/shim split on the
   harness that exists.
2. Make the catalog doctrine capability-named, with the generated
   `references/harness.md` for OpenCode, and capability requirements on engines.
3. Add the capability declaration and tier reporting to the adapter contract
   and `homonto doctor`.
4. Claude Code adapter, aimed at the workflow tier.
5. Codex adapter, at lite tier until its agent limits are verified.
6. Antigravity adapter at projection tier, after its file paths and
   capabilities are verified.

## Open questions

- **Targeting shared declarations.** Do `[mcps]`, `[skills]`, and `[commands]`
  apply to every enabled harness, or does each name its harnesses? And how is a
  harness enabled: an explicit table, or the presence of its native tables?
- **Scope per harness.** Project versus user scope differs (`.claude/` versus
  `~/.claude/`, `.agents/` versus `~/.gemini/`). Does each adapter map homonto's
  `scope` values to its own locations, or are scopes declared natively?
- **Shared instruction files.** Several harnesses read `AGENTS.md`. If homonto
  ever manages it, which adapter owns it?
- **Version pinning.** How is the harness version detected, and what happens
  when it moves past the pinned fixtures: warn, or drop a tier?
