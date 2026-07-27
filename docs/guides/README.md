# Guides

User-facing documentation, one topic per file.

## First steps

- [`getting-started.md`](getting-started.md) — hands-on walkthrough with real
  command output, plus a supported / not-supported matrix. **Start here.**

## Reference

- [`configuration.md`](configuration.md) — every `homonto.toml` table and
  field, defaults, and the fail-fast validation rules.
- [`cli-reference.md`](cli-reference.md) — every `homonto` command: flags,
  exit codes, and examples.
- [`onto-reference.md`](onto-reference.md) — every `onto` command, the phase
  flow, and every entry/exit gate the binary enforces.
- [`to-reference.md`](to-reference.md) — every `to` command: the gate, flags,
  archive naming, and crash safety.

## Concepts

- [`secrets.md`](secrets.md) — `${pass:…}` / `${ENV_VAR}` references and the
  referenced-never-stored guarantees.
- [`projection-and-state.md`](projection-and-state.md) — the apply pipeline:
  surgical merge, symlinked content, state, drift vs. pending, adoption, and
  pruning.
- [`subagents.md`](subagents.md) — the `[subagents.*]` resource: sources,
  link vs. copy mode, scope and targets, per-agent models, and the tool-neutral
  `homonto:` frontmatter block.
- [`remote-source-trust.md`](remote-source-trust.md) — pinned, fail-closed
  remote installs: threat model, verification pipeline, and lifecycle.

## The workflow frameworks

onto and `to` are an **exclusive choice** per repository. onto is for work that
someone else has to pick up or audit — it leaves an archived, gate-stamped
record a stranger can read; `to` is for a fast solo loop that still wants a
real verification pass.

- [`onto-workflow.md`](onto-workflow.md) — concepts: the binary/skills split,
  the five phases, presets, and the specialist subagents.
- [`to-workflow.md`](to-workflow.md) — concepts: the bookkeeper/skills split,
  `plan → do → done`, the plan contract, and the subagents (read-only ones
  concurrent, the single implementer serial).
- [`enforcement.md`](enforcement.md) — making the workflow non-skippable at
  the tool boundary with hooks (`onto doctor --quiet` / `to doctor --quiet`
  plus Claude `settings.json` hooks or an OpenCode plugin).
- [`yagni.md`](yagni.md) — you aren't gonna need it: where each framework
  enforces building only what the change needs now.
- [`kiss.md`](kiss.md) — keep it simple: the simplicity mechanics both
  frameworks encode, for code, plans, and prose.

## When something looks wrong

- [`troubleshooting.md`](troubleshooting.md) — known limitations, gotchas, and
  workarounds for all three binaries.

## Developing homonto itself

Development instructions live in [`../../AGENTS.md`](../../AGENTS.md), with
detail in [`../agents/`](../agents/):

- [`../agents/comet.md`](../agents/comet.md) — the Comet workflow used for big
  development, and why its artifacts are not committed
  ([ADR 0017](../adr/0017-stop-committing-workflow-artifacts.md)). Comet,
  OpenSpec, and Superpowers are external tools the maintainers use; homonto
  does not bundle them ([ADR 0015](../adr/0015-ship-only-onto-frameworks.md)).
- [`../agents/okf.md`](../agents/okf.md) — grounding claims in OKF bundles.
- [`../agents/verification.md`](../agents/verification.md) — the gate and the
  traps worth knowing.
- [`../agents/adr.md`](../agents/adr.md) — when a decision is owed an ADR.

See also [`../personas.md`](../personas.md) for why we build with Comet but
ship onto, [`../adr/`](../adr/) for durable architecture decisions, and
[`../release-checklist.md`](../release-checklist.md) for the release gate.
