# to reference — commands and behavior

The `to` binary's complete command surface. Concepts — the phases, the plan
contract, the skills and subagents, the onto-xor-to exclusivity — are in the
[to workflow guide](to-workflow.md); design rationale is in
[to-framework-design.md](../to-framework-design.md).

## The gate

The mutating commands (`init`, `new`, `phase`, `done`, `abandon`) refuse
until, in order:

1. `homonto.toml` exists at the workspace root,
2. it declares a `[frameworks.to]` table, and
3. `.homonto/catalog/skills/to` exists as a directory (the declaration has
   been applied).

Each failure names the fix (`homonto init`, declare `[frameworks.to]`, run
`homonto apply`). The read-only commands (`status`, `handoff`, `doctor`,
`version`) never read `homonto.toml` and never write.

Change names are lowercase-alphanumeric segments joined by single hyphens
(`fix-login`, `update-deps`); `archive` is reserved.

## Commands

Workspace commands support `--dir <root>`. `init`, `new`, `status`, `phase`,
`done`, `abandon`, and `handoff` also support `--json`. `doctor` instead offers
`--quiet` for exit-code-only checks, while `version` prints plain text and does
not inspect a workspace.

| Command | What it does |
|---|---|
| `to init` | Scaffold `docs/tasks/` + `docs/tasks/archive/` (gated; never overwrites). |
| `to new <name>` | Create a change at phase `plan` with an empty `plan.md` (gated). Only an *active* change blocks a name — archives are date-prefixed, so a finished name is reusable. |
| `to phase <name>` | The one forward transition: `plan → do` (gated). Finishing is `to done`; there is no other advance. |
| `to done <name> --verified [--evidence "<text>"]` | Mark done and archive (gated). `--verified` is **required but self-asserted** — the binary records a checkbox, it observes nothing. `--evidence` records what was asserted, verbatim and unchecked, so a real verification is distinguishable in the archive. Requires phase `do`. |
| `to abandon <name>` | Terminal exit without done; archives (gated). Works from any non-terminal phase. |
| `to status` | Active changes and their phases (a corrupt state file is reported per-entry, not fatal). Read-only, config-independent. |
| `to handoff <name>` | Compact recovery pack: identity, phase, safe next skill, and a plan excerpt (head, complete unchecked task contracts, `Final Verify:`, and bounded notes/verification sections) for resuming after a context compaction. A missing `plan.md` is reported, not silently omitted. Read-only, config-independent. |
| `to doctor [--quiet]` | Workspace health: invalid state files, wedged terminal-but-active changes (an interrupted archive — re-run the finishing command to converge), missing `plan.md`, `do`-phase tasks missing non-empty `Files:`, `Change:`, or `Verify:` fields, a missing or empty `Final Verify:`, non-terminal archive entries, and binary↔framework version skew. These are diagnostics, not transition gates. `--quiet` prints nothing and signals via exit code only — the hook primitive. Read-only, config-independent. |
| `to version` | The release-stamped version. |

## Archive naming

A change finishing on date D archives to `docs/tasks/archive/<D>-<name>/`;
a same-day reuse of the name gets a numeric suffix (`<D>-<name>-2`).
Pre-v0.5.0 unprefixed archive directories are still recognized.

## Crash safety

`done` and `abandon` write the terminal state, then move the directory into
the archive. If that is interrupted, the change is left terminal-but-active:
`to doctor` reports it, and **re-running the same finishing command completes
the archive** (`to done <name> --verified` / `to abandon <name>`), dating the
archive by the recorded finish. Commands that mutate a change (`new`,
`phase`, `done`, `abandon`) take a workspace lock (`docs/tasks/.to.lock`), so
two concurrent sessions fail fast instead of interleaving writes. `init` only
creates the fixed directories idempotently and does not lock. A lock left by
a killed process names its pid; once that pid provably no longer runs, the
next mutating command reclaims the lock itself. A lock with no readable pid
(a crash in the create-to-write window) is removed by hand — a live
session's lock is never stolen.

## What `to` deliberately does not do

No evidence gates (the `--verified` checkbox is an assertion, not a
guarantee — the `to-done` skill is where verification rigor lives), no spec
deltas, no dependency graph, no git awareness, no worktree-per-implementer
orchestration, and no escalation path to onto. If a change needs those, the
repo needs onto.

`to` does run subagents concurrently — the three read-only ones, per
[ADR 0019](../adr/0019-parallelism-follows-write-scope.md). What it does not
do is run two *writers*: `to-implementer` is strictly one at a time, because
`to` keeps a single working tree.
