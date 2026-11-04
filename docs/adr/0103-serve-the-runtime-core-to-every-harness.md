# Serve the runtime core to every harness

- **Status:** Accepted
- **Date:** 2026-11-04

## Context

ADR 0082 puts workflow runtime logic once in the binary; ADR 0090 moved the
draft/publication core there, but the coordinator's `homonto_status` and
`homonto_handoff` tools and the worker handback state (ADR 0077) stayed in the
OpenCode plugin. Claude Code has no plugin runtime of its own to host them: it
launches MCP servers and runs hook commands.

## Decision

- **One core, two transports.** `internal/hostruntime` owns the workflow
  tools, the handback state and the host draft lifecycle. `homonto runtime
  serve` serves them to a plugin shim (OpenCode), which still executes every
  command through its runner and answers native permission prompts.
  `homonto runtime mcp` serves them directly to a harness that launches MCP
  servers (Claude Code): the core runs commands itself, in the config root.
- **The shim keeps what only the harness can do.** On OpenCode: session
  scope, the coordinator check, permission prompts, context injection and the
  status panel. On Claude Code: hook commands (`homonto runtime hook`) that
  inject context, guard coordinator-only tools and relay tool, permission and
  subagent events to the session's core over a private Unix socket keyed by
  config and session id. Claude Code gives MCP servers the session id in
  `CLAUDE_CODE_SESSION_ID`, matching the hooks' `session_id` (2.1.318).
- **Context rendering exists twice, pinned.** OpenCode injects context
  synchronously from its own process, so the shim keeps a TypeScript render;
  the Go render serves hooks. A parity test runs both on the same fixtures and
  fails on any byte of difference, including the continuation policy.
- **Native question dialects.** The draft core returns OpenCode `question`
  arguments or Claude Code `AskUserQuestion` arguments (one to four items).
  On Claude Code, approval correlates the coordinator's PreToolUse with the
  same `tool_use_id`'s PostToolUse answers; a call that carries its own
  `answers` is denied, and a worker's question never correlates.

## Consequences

Every workflow-context install now keeps one `homonto runtime serve` child
alive for the workflow core, in addition to one per hosting workflow. A
missing or skewed binary fails the tools closed (`[runtime_unavailable]`,
`[runtime_mismatch]`), and a handback the core cannot answer is replaced by an
explicit "handback unavailable" notice rather than silence.

On Claude Code the hook relay is a local socket the coordinator's own shell
could reach. Which guarantees that leaves instruction-enforced is decided per
guarantee in the limited-mode policy (ADR 0105), not here.
