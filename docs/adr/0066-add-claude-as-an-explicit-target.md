# Add Claude Code as an explicit target

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The current release supports only OpenCode. A Claude Code port can reuse the
configuration reconciler and the `onto`/`to` workflow engines, but host-specific
entrypoints, permissions, model routes, and runtime APIs differ. Restoring the
historical adapter alone would not restore current workflow behavior.

## Decision

We will develop Claude Code as a separate, explicitly selected `claude` target
alongside `opencode`, not a product fork or new SDK application. Keep the three
existing binaries and shared workflow authority. Omitted targets remain
OpenCode-only; selecting both hosts requires explicit configuration.

Use shared ownership infrastructure and host-independent workflow policy, with
target-specific projection, rendering, and integration. Unsupported capabilities
must fail explicitly rather than silently weaken permissions. Deliver projection
before claiming native workflow support; defer UI parity, GitHub publication
runtime, and permission telemetry. The staged work is in the
[development plan](../claude-target-plan.md); this decision does not mean support
has shipped.

## Consequences

Existing OpenCode configurations do not acquire new destinations or model
requirements. Claude and OpenCode can share workflow records without a second
state machine. The cost is a two-host test matrix and changes to shared catalog,
repository-state, and diagnostic paths that currently assume OpenCode. Live
Claude tests must establish host behavior; rendering tests alone cannot prove
permission enforcement or workflow completion.
