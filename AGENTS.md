# Development Instructions

## Source of truth

The code and its tests define shipped behavior. Nothing else outranks them.
Durable rationale — why a thing is the way it is — lives in [`docs/adr/`](docs/adr/).
When a document and the code disagree, the code wins and the document is wrong;
fix it.

## What this is

Homonto is a workflow orchestrator: it runs governed Task and Change
workflows over AI coding agents, holds the state, enforces the write
boundary, and produces the record. It is not a configuration projector and
has not been one since [ADR 0023](docs/adr/0023-rebuild-homonto-as-workflow-orchestrator.md).

One binary ships from here: `homonto`. Its command surface is pinned by
`internal/cli/surface_test.go` — adding or renaming a command is a
deliberate change to that golden list, never a side effect.

## Two lanes

**Big development** — a new capability, a public API or schema change, or work
that spans modules — runs through Comet. Its artifacts (proposals, delta specs,
tasks, plans, verification reports) are working-tree scratch and are **never
committed**; `openspec/` and `docs/superpowers/` are gitignored. See
[`docs/agents/comet.md`](docs/agents/comet.md).

**Everything else** — bug fixes, mechanical edits, doc updates, repetitive
sweeps — is done directly on a branch with no ceremony. No change directory, no
phase guards. The verification bar below still applies.

Work moves up a lane when it turns out to change public API, storage schema, or
behavior across modules. Say so and switch; do not quietly widen a direct edit.

## What a change leaves behind

If work changed a decision someone could later question, write an ADR. Keep it
short — context, decision, consequences, nothing ceremonial. Format and the
test for whether one is owed: [`docs/agents/adr.md`](docs/agents/adr.md).

Otherwise the commit and its tests are the whole record. Do not manufacture
documents to prove work happened.

## Grounding

Ground claims about this codebase in the OKF bundle rather than guesswork —
`okf_lookup.py` for concepts, generate with `okf_generator.py`. The bundle is
gitignored and may be absent or stale; treat stale as absent, and say which you
used. See [`docs/agents/okf.md`](docs/agents/okf.md).

CodeGraph (`.codegraph/`) and Graphify (`graphify-out/`) are per-developer
artifacts, gitignored and often absent. Use them when present. Neither is
required, and no instruction here depends on them.

Falling back to reading files directly is always acceptable. State which
grounding you used when it affects a conclusion.

## Verification

Add or update focused tests for any behavior change. Run the narrowest command
that actually proves the change, and report its real result — including
failures, skips, and what you did not run.

`./scripts/gate.sh` is the full pre-tag gate; it takes a while, so it is not
the default for routine work. Details and the traps that waste the most time:
[`docs/agents/verification.md`](docs/agents/verification.md).

Never claim a command passed without having run it.

## House rules

- Read the relevant ADRs and the surrounding code before changing behavior.
- Keep changes focused. Do not revert or restructure unrelated work.
- This repo does **not** run Homonto on itself. There is no `.homonto/` and
  no generated `.claude/` or `.opencode/` content; `.claude/` and
  `.opencode/` hold each developer's own setup and are gitignored. Do not
  create a workspace here — the tests create their own in temporary
  directories, which is where a workspace under test belongs.
- Refusals are the product. When a change makes something that used to be
  refused succeed, that is a behavior change needing a test and probably an
  ADR, not a fixed bug.
