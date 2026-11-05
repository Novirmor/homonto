# Harness architecture — design document

Status: **implemented for OpenCode and Claude Code** (M4). The decision is
[ADR 0082](adr/0082-support-harnesses-natively-in-tiers.md); the runtime core
split is [ADR 0103](adr/0103-serve-the-runtime-core-to-every-harness.md), the
Claude Code adapter [ADR 0104](adr/0104-project-claude-code-natively.md), and
the accepted Claude Code limitations
[ADR 0105](adr/0105-accept-limited-claude-code-capabilities-per-guarantee.md).
Codex and Antigravity remain design only. This document
describes how homonto supports more than one agent harness: OpenCode today,
then Claude Code, Codex, and Google Antigravity (`agy`). Where it says
"verify", the capability has not been checked against a pinned harness
version and must be before that harness is assigned a tier.

## Principles

1. **OpenCode first.** OpenCode is the primary harness: every feature is
   designed, shipped, and verified there first. Other harnesses follow at
   their tier, and their gaps are handled in their adapters. OpenCode never
   waits for them and never loses a feature to them.
2. **Native in, native out.** A harness is configured in its own vocabulary
   and rendered in its own formats. There is no neutral config schema and no
   translation of one harness's settings into another's.
3. **Capabilities, not names.** Engines declare what they need; adapters
   declare what a pinned harness version provides. Support follows from the
   match.
4. **Fail closed.** A declaration a harness cannot render, or an engine whose
   needs a harness cannot meet, is a plan or selection error that names the
   gap. Nothing is dropped or approximated silently.
5. **One runtime, thin shims.** Workflow runtime logic lives once in the Go
   binary. A harness integration carries only what that harness needs to reach
   it.
6. **Stale support is worse than none.** The v0.13.0 removal happened because
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
       permission handback) ── served by the binary to every harness
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
adapter links or copies it to the harness's skill location as-is. Shared
declarations (`[skills]`, `[commands]`, `[subagents]`, packages) go to the
top-level `harnesses` list unless they set their own `targets` (ADR 0104).

User MCP servers are not configuration either. They are bring-your-own
([ADR 0085](adr/0085-bring-your-own-mcp-servers-and-skills.md)): `[mcps.*]`
fails at load, and no adapter reads, lists or rewrites a user's servers. The
one MCP server homonto registers is its own runtime core (next sections).

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
| **Projection** | No workflows; config, skills, and agents projected | The resources it renders |

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

The workflow runtime core is Go (`internal/hostruntime`, ADR 0103): the
coordinator-only `homonto_status` and `homonto_handoff` tools, the hosting
draft/approve/publish lifecycle and its providers (ADR 0090), and the worker
permission handback state (ADR 0077). The binary serves it two ways:

- **OpenCode:** the bundled `homonto-workflow` plugin starts
  `homonto runtime serve` as its child and talks to it over stdio. The plugin
  is the shim: session scope, the coordinator check, native permission
  prompts, context injection at request and compaction, and the status panel.
  Nothing is registered under the `mcp` key of `opencode.jsonc`.
- **Claude Code:** Claude Code launches `homonto runtime mcp` as an MCP stdio
  server. Hook commands (`homonto runtime hook`) are the shim: context
  injection, the coordinator-only tool guard, agent policies, and relaying
  permission and subagent events to the session's core.

**Runtime MCP registration**
([ADR 0107](adr/0107-register-the-runtime-mcp-server-as-homontos-own.md)).
On a harness that launches MCP servers, homonto registers exactly one server,
named `homonto`, with a fixed command (`homonto runtime mcp --harness <h>
--config <selected config> [--host <id>...]`), no `env` and no secrets. It
owns only that one key in the harness's MCP file (`mcpServers.homonto` in
`.mcp.json` for Claude Code). The user's servers in the same file are never
read, listed, rewritten or pruned. Apply creates the entry while the harness
is targeted and the workflow runtime is enabled, and removes it when either
stops. Status reports its drift, and `homonto doctor` reports whether it is
registered and whether `homonto` is on `PATH`. Enabling it on Claude Code is
the user's folder-trust step: homonto never trusts a folder.

**Approval stays native.** Publication approval uses the harness's
`question-dialog`. The core never invents its own approval channel.

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

Each step ships on OpenCode first; a later harness picks it up when its adapter
reaches that tier.

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

Resolved by ADR 0104: targeting shared declarations (`harnesses`, with a
resource's own `targets` winning) and how a harness is enabled. Resolved for
Claude Code by ADR 0104: scope per harness and version pinning; these remain
open for Codex and Antigravity. MCP is settled by ADR 0085 and ADR 0107: user
servers are bring-your-own, and the runtime server is homonto's own. The
shared instruction file question remains open.

- **Scope per harness.** Project versus user scope differs (`.claude/` versus
  `~/.claude/`, `.agents/` versus `~/.gemini/`). Does each adapter map homonto's
  `scope` values to its own locations, or are scopes declared natively?
- **Shared instruction files.** Several harnesses read `AGENTS.md`. If homonto
  ever manages it, which adapter owns it?
- **Version pinning.** How is the harness version detected, and what happens
  when it moves past the pinned fixtures: warn, or drop a tier?
