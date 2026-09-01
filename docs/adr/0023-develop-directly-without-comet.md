# Develop directly, without Comet

- **Status:** Accepted
- **Date:** 2026-09-01

## Context

ADR 0012 adopted Comet + OpenSpec + Superpowers as this repository's
big-development workflow. ADR 0017 kept the stack but stopped committing
anything it produces, which left the repo's residue thin: a tracked
`.comet/config.yaml`, the operator doc `docs/agents/comet.md`, and the
two-lane routing in AGENTS.md.

By then what the stack actually contributed here was a list of documented
traps — a build probe that does not know Go, an archive guard that fails on
dangling state, a transient state file that leaked into commits — while the
durable record it promised was always the ADR, which is written anyway. The
stack is external and unenforced: nothing mechanical ever gated a change on
it.

## Decision

We will develop directly: branches, focused commits, focused tests, and an
ADR when a decision is owed. Comet, OpenSpec, and Superpowers leave the
repository — `.comet/` and `docs/agents/comet.md` are deleted, AGENTS.md
drops the two-lane routing, and the contributor docs stop naming an external
workflow. Dogfooding onto (or `to`) remains deferred to v1, unchanged.

## Consequences

- Supersedes ADR 0012 (workflow adoption). ADR 0017's rule — workflow
  artifacts are never committed — stands for anyone running an external
  stack locally; `openspec/`, `docs/superpowers/`, and `.superpowers/` stay
  gitignored, and `.comet/` joins them.
- Nothing mechanical is lost: Comet gated nothing. The verification bar
  (focused tests, `./scripts/gate.sh` before a tag) and the ADR record are
  unchanged.
- `scripts/agents-doc-check.sh` no longer requires `docs/agents/comet.md`.
