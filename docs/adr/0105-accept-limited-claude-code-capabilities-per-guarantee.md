# Accept limited Claude Code capabilities per guarantee

- **Status:** Accepted
- **Date:** 2026-11-04

## Context

ADR 0082 tiers harnesses and fails closed on a missing capability, and
ADR 0097 let only `enforced` satisfy a requirement. Claude Code provides every
capability the workflow tier needs, but three only with gaps OpenCode does not
have. Neither blanket parity (refuse Claude Code) nor blanket acceptance
(pretend the gaps are not there) was agreed (#70). The threat model for the
engines is the honest agent ([ADR 0021](0021-onto-is-for-handoff-and-audit.md)): a guarantee must
stop an honest agent from skipping a step, not defeat a hostile one.

## Decision

A capability may be declared `limited`: provided, with a named gap this ADR
accepts. `limited` satisfies a requirement; doctor and the generated harness
map name the gap. Per guarantee, on Claude Code 2.1:

| Guarantee | Capability | Decision |
|---|---|---|
| Read-only skeptics and reviewers | `agent-tool-limits` | Parity: `disallowedTools` removes edit, shell and dispatch tools. Enforced. |
| Workers cannot reach runtime tools | `agent-tool-limits` | Parity: `disallowedTools: mcp__homonto` plus the PreToolUse guard on any `agent_id`. Enforced. |
| Only the coordinator publishes | `agent-tool-limits` | Enforced when the session runs as the coordinator (projected `agent`); a main-thread session reporting another `agent_type` is denied. A session reporting no agent at all is not judged. Accepted. |
| Worker shell rules | `shell-rules` | **Limited, accepted.** The policy hook applies OpenCode's last-match-wins rules to the raw command; a session started without the coordinator agent carries no policy. |
| Workflow records stay coordinator-owned | `agent-tool-limits` | Parity: path denies for Read/Edit/Write and friends. Shell writes are not sandboxed, as on OpenCode. |
| Spawn lists | `agent-tool-limits` | Parity: the hook denies dispatch of an agent outside the list. |
| Decisions go through the native dialog | `question-dialog` | **Limited, accepted.** `AskUserQuestion` takes at most four questions, so a publication draft holds at most four items; it is absent in `claude -p`, where a required decision is reported pending rather than asked another way. |
| Publication needs the user's correlated answer | `question-dialog` | **Limited, accepted.** Approval correlates the coordinator's PreToolUse and PostToolUse of the same `tool_use_id`, relayed over a private local socket; a pre-answered question is denied and `Bash(homonto runtime:*)` is denied. A shell-capable agent that deliberately bypassed those rules could forge the relay; that is outside the honest-agent model. |
| Continuation policy and recovery snapshot | `context-hooks` | Parity for what the capability promises: injected at startup, resume, clear and compaction. Not refreshed per request as on OpenCode; `homonto_status` reads it mid-session. |
| Worker permission handback | `permission-events` | **Limited, accepted.** Claude Code reports a worker's prompt but not the answer; a prompt whose tool never ran before the worker stopped is handed back as refused. Auto-mode denials are exact (`PermissionDenied`). |
| Status panel and idle notifications | `status-ui` | Absent, accepted: optional everywhere (ADR 0082). |

## Evidence

Observed on Claude Code 2.1.318 (2026-11-05) by the `policy/claude` matrix
cell, which logs every hook call:

- The main thread reports the session agent as `agent_type` (`homonto`), so the
  coordinator check and the coordinator's policy apply. A worker's calls carry
  its `agent_id` and `agent_type`.
- A `deny` from the PreToolUse hook stops a worker's tool call, and an `allow`
  lets it run without a prompt in `claude -p`.
- A worker's permission prompt arrives as `PermissionRequest` and its stop as
  `SubagentStop`. With no answer reported, the refused prompt is handed back
  with the dispatch result. When Claude Code launches the worker in the
  background (`status: "async_launched"`), the refusal is handed back on the
  coordinator's next turn, when the worker's completion arrives.

## Consequences

Every builtin engine and hosting workflow runs on Claude Code at the workflow
tier, and `homonto doctor` lists the three limited capabilities with their
gaps. A new gap is a new row here, not a silent change to the declaration. If a
later Claude Code release closes a gap, the capability moves to `enforced` and
its row says so. A guarantee the honest-agent model cannot cover would not be
accepted as `limited`; it would keep the engine off the harness.
