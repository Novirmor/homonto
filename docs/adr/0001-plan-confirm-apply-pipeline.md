# Adopt a terraform-style plan/confirm/apply pipeline with tool adapters

- **Status:** Superseded by 0023
- **Date:** 2026-07-03
- **Change:** homonto-v1-core

## Context

homonto writes into real user tool configuration (Claude Code, OpenCode).
Blind writes risk destroying hand-maintained config; users need to see
exactly what would change before anything is touched, and adding future
tools must not require engine rework.

## Decision

We will parse `homonto.toml` into one tool-agnostic desired-state model and
drive every tool through an adapter interface (`Read → Plan → Apply`).
`plan` renders a terraform-style diff and writes nothing; `apply` re-plans,
asks for confirmation (unless `--yes`), then executes. Adding a tool is one
new adapter, no engine changes.

## Consequences

- Users always get a dry run; destructive surprises are structurally hard.
- Every feature must be expressible as plan changes — anything invisible in
  the plan is a bug (see the owned-skill link fix in add-onto-workflow).
- Two adapters (claude, opencode) share planning idioms via small helpers;
  per-tool schema differences stay inside each adapter.
