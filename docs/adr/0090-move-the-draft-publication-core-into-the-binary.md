# Move the draft/publication core into the binary

- **Status:** Accepted
- **Date:** 2026-11-02

## Context

`github.ts` in the OpenCode workflow plugin mixed two concerns: a host-neutral
draft lifecycle (session identity, native-question approval, TTL, capacities,
invalidation, freshness, uncertain-send reconciliation; ADR 0059, ADR 0060)
and GitHub transport. GitLab.com and Bitbucket Cloud need the same lifecycle
(roadmap M1). The target architecture puts the runtime core in Go behind MCP
(harness-architecture.md § Runtime, M4.1). Extracting a provider boundary in
TypeScript would have produced a second framework for M4 to undo.

## Decision

The lifecycle and its host providers live in Go, in `internal/hostruntime`,
and are served by `homonto runtime serve`. It speaks MCP-shaped JSON-RPC 2.0
over stdio: `initialize`, `tools/list`, `tools/call` and
`notifications/cancelled`, plus `homonto/*` methods for the parts MCP has no
channel for.

- **Provider boundary.** `Provider` covers target and origin parsing,
  repository and actor identity, destination snapshots, payloads, the
  publication command, publication, and receipt reconciliation. GitHub is the
  first provider. The lifecycle has a fake-provider test.
- **The shim stays the harness.** The OpenCode plugin starts the core, relays
  tool calls, harness events and native `ask`, and mirrors per-session draft
  state for synchronous context injection. Approval stays native (ADR 0059).
- **Commands run in the shim.** The core never executes a process. Every
  command goes back to the plugin's runner (`homonto/run`), so the runner's
  limits, abort wiring, and the coordinator's host-CLI-only posture (ADR 0047)
  do not change. When M4.1 serves the core to harnesses directly, it takes over
  execution.
- **The harness clock rules.** Every shim message carries `now`, so expiry
  follows the harness's clock exactly as it did in-process.
- **Per-host tool names.** Tools stay `homonto_<host>_draft/status/publish`
  (`homonto_github_*` unchanged), and permissions are `homonto_<host>_read`.
  Permission rules and approval headers are naturally per host. The host
  skill names its tools, so shared process skills stay host-neutral
  (ADR 0089).
- **Skew fails closed.** The shim compares the core's `tools/list` with its own
  definitions at startup. A missing or mismatched `homonto` fails with
  `[runtime_unavailable]` or `[runtime_mismatch]`.

## Consequences

The plugin now needs a `homonto` on PATH that has `runtime serve`. The plugin
and binary ship together, since apply materializes the plugin from the same
binary, so a mismatch means a stale install, and it fails closed. Each plugin
instance with a hosting workflow keeps one `homonto` child process alive.

The existing JavaScript contract tests run unchanged against the Go core.
Tests that load the plugin now build `homonto` first (`internal/testbin`).
A command aborted mid-run now reports `[invocation_cancelled]` instead of the
runner's abort message.

Status, handoff and handback state remain in TypeScript until M4.1.
