# Support harnesses natively, in tiers

- **Status:** Accepted (architecture only; nothing implemented yet)
- **Date:** 2026-10-28
- **Design:** [`docs/harness-architecture.md`](../harness-architecture.md)

## Context

homonto targets OpenCode only. Claude Code, Codex, and Google Antigravity
(`agy`) are planned. Claude Code and Codex were supported once and removed in
v0.13.0: their config projections kept working while the workflows became
OpenCode-only, so the support went stale. The projector is already pluggable
(an `Adapter` interface, a registry, and shared `structproj`/`fileproj`/`copyproj`
control flow, ADR 0014). What is not portable is the workflow runtime (the
OpenCode TypeScript plugin) and the doctrine, which names OpenCode's tools.

## Decision

- **OpenCode first.** OpenCode is the primary harness. Features are designed,
  shipped, and verified on OpenCode first; other harnesses follow at their tier.
  OpenCode work never waits on another harness, and another harness's limits
  never cut an OpenCode feature.
- **Native configuration, no neutral schema.** Each harness is configured in
  its own vocabulary, under its own keys. homonto does not translate one
  harness's settings into another's.
- **Native rendering.** Everything homonto writes for a harness — config
  files, agent and command files, permissions, hooks, plugins — is that
  harness's native format, produced by that harness's adapter.
- **Tiered support.** A harness is supported at one of three tiers: workflow,
  lite, or projection. The tier follows from the capabilities the adapter
  declares for a pinned harness version, not from the harness's name.
- **Fail closed on missing capability.** An engine runs only on a harness that
  provides the capabilities it needs. A declaration a harness cannot render is a
  plan error naming the gap, never silently dropped or approximated.
- **One runtime core.** Workflow runtime logic (status, handoff, GitHub drafts,
  permission handback) is implemented once in the homonto binary. Per-harness
  shims carry only what the harness integration requires.

## Consequences

Adding a harness means an adapter, its capability declaration, its renderers,
and its runtime shim, not changes to the engines. Users configure each harness
in its own terms, so a setting shared by two harnesses is declared twice. The
doctrine must name capabilities instead of OpenCode tools before a second
workflow-tier harness can ship. Nothing changes for OpenCode until
implementation starts; sequencing and open questions are in the design doc.
