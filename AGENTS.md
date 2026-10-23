# Development Instructions

## Source of truth

The code and its tests define shipped behavior. Nothing else outranks them.
Durable rationale — why a thing is the way it is — lives in [`docs/adr/`](docs/adr/).
When a document and the code disagree, the code wins and the document is wrong;
fix it.

## How work is done

Directly on a branch, with no external workflow stack
([ADR 0023](docs/adr/0023-develop-directly-without-comet.md)): focused
commits, focused tests, and an ADR when a decision is owed (see below).
There is no change directory and no phase machinery. The verification bar
below always applies.

## What a change leaves behind

If work changed a decision someone could later question, write an ADR. Keep it
short — context, decision, consequences, nothing ceremonial. Format and the
test for whether one is owed: [`docs/agents/adr.md`](docs/agents/adr.md).

Otherwise the commit and its tests are the whole record. Do not manufacture
documents to prove work happened.

## Grounding

CodeGraph is the one local code index
([ADR 0066](docs/adr/0066-ground-development-in-codegraph.md)). Use it for
"where is X", "who calls X", and "what does changing X touch" instead of
guessing. `task index` builds or refreshes it, and `task affected` lists the
tests your changes touch. `.codegraph/` is per-machine and gitignored; refresh
it before trusting it, and treat a stale index as absent. See
[`docs/agents/codegraph.md`](docs/agents/codegraph.md).

Falling back to reading files directly is always acceptable. State which
grounding you used when it affects a conclusion.

## Tooling

`task` lists the developer tasks ([`Taskfile.yml`](Taskfile.yml)). go.mod pins
the Go toolchain; `task tools` installs the pinned golangci-lint. Pre-commit
hooks are in `.pre-commit-config.yaml` (`pre-commit install`).

## Verification

Add or update focused tests for any behavior change. Run the narrowest command
that actually proves the change (`task test-changed` is a good default; run
`task check` before pushing), and report its real result — including failures,
skips, and what you did not run.

`./scripts/gate.sh` is the full pre-tag gate; it needs Docker and takes a while,
so it is not the default for routine work. Details and the traps that waste the
most time: [`docs/agents/verification.md`](docs/agents/verification.md).

Never claim a command passed without having run it.

## House rules

- Read the relevant ADRs and the surrounding code before changing behavior.
- Keep changes focused. Do not revert or restructure unrelated work.
- This repo does **not** dogfood homonto. There is no root `homonto.toml`, no
  `.homonto/`, and no projected `.claude/` or `.opencode/` content. Do not
  create them; `.claude/` and `.opencode/` hold each developer's own setup and
  are gitignored.
- Three binaries ship from here: `homonto` (root), `onto` (`cmd/onto`), and
  `to` (`cmd/to`). A change to shared internals affects all three.
