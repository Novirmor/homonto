# Running Comet here

Comet drives **big development** in this repository: new capabilities, public
API or schema changes, and work spanning modules. Smaller work does not use it
(see [`../../AGENTS.md`](../../AGENTS.md)).

OpenSpec owns WHAT (proposals, requirements, delta specs, archive semantics).
Superpowers owns HOW (technical design, plans, execution, verification). Comet
state binds the two.

Comet, OpenSpec, and Superpowers are **external** tools the maintainers use.
Homonto neither bundles nor requires them: what this repository is built with
and what it ships are separate questions, and they have never matched.

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

## The CLI moves; check it before improvising

Comet is an external tool that evolves independently of this repository,
and skill text shipped with it can lag the installed binary (or lead it).
When a skill instruction names a subcommand that fails, read
`comet --help` for the installed surface before working around it — the
binary's own help is the only authoritative, version-matched reference.
`.claude/` is gitignored and per-developer, so the skills exist only if
you installed them yourself.

## Traps

**Comet's guards know npm/Maven/Cargo, not Go.** A build or verify guard
can be wrong about this repository's toolchain. Verify independently —
`go build ./...`, `go vet ./...`, the tests — and never let a guard's
build probe stand in for actually running them.

**Phase guards are order-sensitive.** Tick every `tasks.md` item before the
build guard; do not set verify-phase fields while still in an earlier
phase. The `build → verify` transition resets `verification_report` and
`branch_status`, so set those after the build guard, immediately before
the verify guard.

**`.comet/current-change.json` is transient state that gets committed by
accident.** It is the selection record and easily lands in a feature commit
carrying a stale branch name. It should not be tracked.
