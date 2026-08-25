# Adopt onto: a self-contained markdown development workflow

- **Status:** Superseded by 0023
  Comet + OpenSpec + Superpowers workflow on 2026-07-09 (ratified in
  `docs/adr/0012-readopt-comet-openspec-workflow.md`). onto remains a shipped
  product framework; this ADR's decision applied only to this repo's own
  development process.
- **Date:** 2026-07-04
- **Change:** add-onto-workflow

## Context

homonto development previously ran on external workflow machinery: a
spec-lifecycle CLI, bash guard/state scripts, and artifacts split across two
trees. That machinery is not portable, not self-describing, and its
documentation model (design docs and reports in a side tree) does not match
how this project wants to document itself: ADRs, living capability specs,
and user-facing guides written after implementation.

## Decision

We will develop through **onto**: eight markdown-only skills (dispatcher,
five phases open→design→build→verify→close, fix/tweak presets) shipped as
homonto-owned content and dogfooded via `homonto apply`. All artifacts live
in one `docs/` tree (adr/, specs/, changes/, guides/). Phase state is an
agent-managed `state.yaml` per change; verifiable file state is the source
of truth and the dispatcher re-derives the phase on every invocation.
rtk and graphify are recommended preflight tools, but ADR 0008 changed their
absence from a hard halt into a warn-and-proceed condition. Issue/PR intake
skills act as entry points, while PR creation/review stay outside the workflow.

## Consequences

- Nothing to install for the workflow itself; it travels with the repo and
  works in any repo that adopts the `docs/` layout.
- No hard guard enforcement — mitigated by explicit exit checklists per
  phase, evidence-based verification, and file-state-wins recovery.
- State drift is possible but self-healing (derivation cross-check).
- The close phase makes documentation (spec merge, ADR acceptance, guides)
  a blocking obligation, not an afterthought.
