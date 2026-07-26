# Running Comet here

Comet drives **big development** in this repository: new capabilities, public
API or schema changes, and work spanning modules. Smaller work does not use it
(see [`../../AGENTS.md`](../../AGENTS.md)).

OpenSpec owns WHAT (proposals, requirements, delta specs, archive semantics).
Superpowers owns HOW (technical design, plans, execution, verification). Comet
state binds the two.

Comet, OpenSpec, and Superpowers are **external** tools the maintainers use;
homonto does not bundle them ([ADR 0015](../adr/0015-ship-only-onto-frameworks.md)).
[`../personas.md`](../personas.md) explains why this repo builds with Comet but
ships onto.

## Artifacts are scratch, not content

`openspec/` and `docs/superpowers/` are **gitignored**. Proposals, delta specs,
tasks, plans, and verification reports record how a change was produced, not
what the code does — they went stale faster than anyone reconciled them, and a
stale spec that reads as authoritative is worse than no spec.

Consequences worth internalizing:

- **The archive step is local bookkeeping.** It merges delta specs into your
  working-tree `openspec/specs/` and moves the change directory. It produces
  **no archive commit**, because none of those paths are tracked.
- **The commit that matters is the code, its tests, and an ADR** if a decision
  was made. That is the entire durable record.
- Do not `git add -f` an openspec or superpowers path to "preserve" it. If it
  is worth keeping, it belongs in an ADR or a guide.

## Quick start

- New work: `/comet <what you want to build>`
- Resume: `/comet`
- Existing-behavior bug: `/comet-hotfix <symptom>`
- Copy/config/docs-scale change: `/comet-tweak <change>`

Phases run open → design → build → verify → archive. Each has blocking user
decisions — requirements and change name, design approach, plan-ready workflow
configuration, verify failures, branch handling, archive confirmation. Do not
infer these from defaults or from what was chosen last time.

## The CLI on PATH is not the whole CLI

The installed `comet` binary (0.4.0-beta.3) exposes only `init`, `status`,
`dashboard`, `doctor`, `update`, `uninstall`, `eval`, `skill`, `publish`,
`creator`, and `bundle`. It has **no `workflow`, `state`, `guard`, or
`archive`** subcommand, so every skill instruction written as `comet state …`
or `comet guard …` fails with `unknown command`.

Use the bundled scripts instead — this is the documented compatibility path in
the skill's own `reference/scripts.md`, not a workaround:

```bash
DIR="$(node .claude/skills/comet/scripts/comet-env.mjs)"
node "$DIR/comet-state.mjs"  <change> <get|set|select|check|transition|next>
node "$DIR/comet-guard.mjs"  <change> <phase> [--apply]
node "$DIR/comet-archive.mjs" <change>
```

`/comet`'s own `comet workflow resolve` also fails; resolve the entry with
`node .claude/skills/comet/scripts/comet-entry-runtime.mjs . --json`, which
returns `classic` for this repo.

`.claude/` is gitignored and per-developer, so these paths exist only if you
installed the skills yourself.

## Traps

**`COMET_SKIP_BUILD=1` is needed for both the build and the verify guard.**
Comet's build probe recognizes npm/Maven/Cargo, not Go, so both guards fail on
"Build/Verification passes" even when the tree is green. Verify independently
(`go build ./...`, `go vet ./...`, tests), then run the guard with the variable
set.

**The archive guard fails on a dangling `handoff_context`.** `comet-archive`
moves the change directory but never rewrites `handoff_context` in the archived
`.comet.yaml`, so the guard reports the design-context path "does not exist on
disk". Every archived change reproduces this — it is a tool quirk, not a defect
in your change. Repoint the field at the archive path and re-run the guard.
State `set`/`guard` resolve an archived change by its **original** name.

**Phase guards are order-sensitive.** Tick every `tasks.md` item before the
build guard; do not set verify-phase fields while still in an earlier phase.
The `build → verify` transition resets `verification_report` and
`branch_status`, so set those after the build guard, immediately before the
verify guard.

**`.comet/current-change.json` is transient state that gets committed by
accident.** It is the selection record and easily lands in a feature commit
carrying a stale branch name. It should not be tracked.
