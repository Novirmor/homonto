# The onto workflow

**onto** is a spec-driven development workflow that homonto ships as a
bundled framework. It has two halves that work together:

- the **`onto` binary** (built from `cmd/onto/`, installed beside
  `homonto`) — the deterministic operator that creates change workspaces,
  gates phase transitions, merges spec deltas, and archives completed
  changes; and
- the **`onto-*` skills** (materialized from the builtin catalog by
  `homonto apply`) — the agent-facing process prose that drives the work
  inside each phase.

The binary owns the *state and the gates*; the skills own the *work*. A
change moves through five phases in a fixed order:

```
open → design → build → verify → close
```

`close` is terminal; `onto close` then archives the change. Each change
tracks its phase and gate evidence in an `onto-state.yaml` inside its
workspace directory, always written through the binary and never by hand.

This guide covers the concepts. The precise command surface and every gate:
[onto reference](onto-reference.md). Making the gates non-skippable at the
tool boundary: [enforcement](enforcement.md).

## Install and enable

`onto` is version-stamped and installs alongside `homonto`:

```bash
go install github.com/noviopenworks/homonto/cmd/onto@latest
onto version            # prints: onto <version>
```

The mutating commands (`init`, `new`, `set`, `advance`, `close`, `abandon`,
`merge-deltas`) require the onto framework to be **declared and applied
through homonto first**. This is how the skills land in your tools:

```toml
[frameworks.onto]
source = "builtin:onto"
scope = "project"
# plus a [subagents.<name>.<tool>] model block per onto agent — see the
# configuration reference
```

Then `homonto apply`. The read-only commands (`status`, `state`, `gate`,
`scale`, `graph`, `dirt`, `handoff`, `doctor`, `version`) run without any of
this: they never read `homonto.toml` and never write.

`homonto apply` also installs the framework's **slash commands** into each
tool: `/onto` (the dispatcher — it derives the active change's real phase
and routes automatically), plus `/onto-open`, `/onto-design`, `/onto-build`,
`/onto-verify`, `/onto-close`, `/onto-fix`, `/onto-tweak`, and
`/onto-no-slop`. Each command loads its matching `onto-*` skill; every state
change still goes through the binary.

## The layout

`onto init` scaffolds four directories under the workspace root,
idempotently — existing content is never overwritten:

```
docs/
├── changes/                # change workspaces + archive
│   ├── <name>/             # active change (onto-state.yaml, proposal, …)
│   └── archive/YYYY-MM-DD-<name>/
├── specs/                  # living capability specs
├── adr/                    # accepted / superseded decisions
└── guides/                 # user-facing docs
```

## Phase walkthrough

The `onto-*` skills carry the process discipline inside each phase; the
binary gates the transitions between them. Since v0.9.0 the judgment gates
also carry **evidence tokens** — a recorded `onto set` answer the binary
refuses to advance without, so an honest agent cannot skip a user gate:

| Token | Gate it records | Refused without it |
|---|---|---|
| `proposal-approved` | open's artifact review (full) | `advance` out of open |
| `approach-confirmed` | design's approach choice (full) | `advance` design → build |
| `close-confirmed` | close's final confirmation (all) | `merge-deltas`, `close` |

`onto gate <name> --json` lists whichever are still pending with the exact
setter. Leaving verify additionally cross-checks `verification.md`'s
`Result:` line against the recorded `verify.result` — the report and the
state must agree. **Migration note:** a full change already in flight when
you upgrade will refuse its next `advance` until the missing token is
recorded — that is the unanswered gate being re-asked, not a fault; answer
it and record the token. `onto state <name> --json` now also derives the
**working phase** from the artifacts (`derived_phase`, with
`phase_mismatch` when the claim disagrees), so skills route on tested
derivation instead of re-reading the evidence table by hand.

- **open** — clarify the requirement, decide whether the work should split
  into several changes, and create the workspace (`onto new`).
- **design** — ground-truth exploration, 2–3 candidate approaches, user
  confirmation, then `design.md`, ADR drafts (unnumbered,
  `Status: Proposed`), delta specs with testable scenarios, and the task
  list derived from the confirmed design. No implementation code in this
  phase.
- **build** — `plan.md` giving each `tasks.md` item its detail under a
  matching `## Task N.M` heading (files, what to do, how it is verified),
  one commit per task, root-cause-first debugging on any failure. The task
  list is **live state**: discovered work is appended to both files before
  its code is written. `tasks.md` holds the checkboxes — `plan.md` carries
  no completion state — and each checkoff rides its task's own commit, so a
  fresh session resumes from the first unchecked item. Entering build requires an
  isolation choice (`branch` or `worktree`); build work is never committed
  unisolated.
- **verify** — scale-appropriate check of every delta-spec scenario with
  fresh command output as evidence, recorded in `verification.md`.
  `onto scale` derives the appropriate verification level from the measured
  diff.
- **close** — `onto merge-deltas` merges the change's delta specs into
  `docs/specs/` deterministically, then `onto close` archives the workspace
  once all evidence gates pass. Number and accept ADRs into `docs/adr/`, and
  update the affected guides.

Two **presets** run a reduced path for small work: `onto new --workflow fix`
(an existing-behavior bug) and `--workflow tweak` (copy/config/docs-scale
change) go `open-lite → build → verify → close`, skipping design, and
upgrade to the full path when scope grows. A preset reaches build in one
gated call — `onto advance <name> --to build` — instead of two ceremonial
advances; presets are exempt from the two full-only tokens but not from
`close-confirmed`. `onto abandon` is the
unsuccessful terminal state for work that stops rather than completes.

## Specialist subagents

`homonto apply` installs the framework's agents. Do not also declare them in a
top-level `[subagents.*]` table; the names collide.

onto ships **five** agent definitions: the `onto` orchestrator plus four
specialists the skills delegate to.

- **`onto`** — the orchestrator, and the one agent that is not a specialist.
  It is declared `primary: true`, which renders as OpenCode's
  `mode: primary`, where the `/onto` command carries `agent: onto` and routes
  into it. The agent prompt deliberately does not restate the skill, so the
  two cannot drift.

- **`onto-explorer`** — read-only; reads across many files (and keeps bash
  for the code-intelligence CLIs and git history) to answer "how does X work
  / where does behavior live", returning conclusions rather than dumps. Used
  for grounding in open and design.
- **`onto-reviewer`** — read-only; reviews a diff for correctness, security,
  contract, and clarity, ranked by severity. Used per task in build and
  across the diff in verify.
- **`onto-implementer`** — edit-capable executor. It
  executes one bite-sized task from a precise spec and returns a diff. It
  does not plan or judge scope, and it reports discovered work rather than
  doing it.
- **`onto-skeptic`** — read-only adversarial verifier
  used in the verify phase. It is dispatched **at least twice
  in parallel**, one lens each — `conformance` (refute each scenario's
  evidence) and `robustness` (attack the gaps the scenarios never cover) are
  mandatory in full mode, and a change may earn further lenses — and is
  prompted to **refute, never approve** (ADR 0007). It keeps bash so it can
  re-run the evidence itself, and it is read-only so it can never fix what
  it finds. That independence is the point.

Planning, judging scope, deciding, and every `onto` binary call stay with the
orchestrator, because those steps are gated on user confirmation and a
subagent cannot prompt. Who does the *editing* depends on the change's
`build_mode` field (`onto set build-mode <change> direct|subagent`): under
`direct` the orchestrator does it, and under `subagent` the implementer edits
and commits its own task's files.

All declare their capabilities once in a tool-neutral `homonto:` frontmatter
block, rendered into OpenCode's `permission:` map (see
[subagents](subagents.md)). Parallelization follows what an agent writes
rather than which agent it is
([ADR 0019](../adr/0019-parallelism-follows-write-scope.md)): the three
read-only agents run concurrently wherever the work is independent — grounding
in open and design, per-task and per-lens review in build, scenario evidence
and the skeptic lenses in verify. The edit-capable implementer runs one at a
time unless each has its own git worktree and a disjoint file set, which is
what `isolation: worktree` is for. Dialogs belong to the orchestrator alone:
a subagent that needs a decision returns a `Questions:` section and stops,
and the orchestrator asks, then re-dispatches with the answer. That protocol
is the real guarantee — the rendering backs it in OpenCode, where
`dialogs: false` becomes `question: deny`. Gate decisions are asked through
an interactive dialog (`onto gate --json` supplies the structured decision;
the skill renders it). The orchestrator — your main session — still owns
every edit and commit.

## Working in a dirty tree

Uncommitted work is normal: an interrupted task, a parallel change, your own
edits. onto classifies it rather than treating "dirty" as one condition.
`onto dirt [change] [--json]` reports every uncommitted path in three
classes. A change created with `onto new --repo <declared-name>` additionally
audits those selected sibling repos and labels their entries; all external
entries are `source` and block close. An unselected declared repo is not part
of the change and does not block it:

| Class | What it is | Blocks this change's close? |
|---|---|---|
| `own` | the change's own `docs/changes/<name>/` artifacts | **yes** — its evidence must be committed |
| `change` | another change's docs, or the archive | no — that change's own close gate owns it |
| `source` | any other path in the repo | **yes** — until it is attributed and committed |

That split lets two changes be in flight at once: one change's half-written
proposal no longer blocks another change's close. When close *is* blocked,
the refusal names the offending paths instead of a bare "dirty worktree".

The division of labor is deliberate. The **binary** owns what-is-dirty and
what-blocks-close (structure, not judgment); the **agent** owns attribution,
deciding whether a `source` diff belongs to the current change, belongs
elsewhere, or is unclear enough to stop and ask. The skills follow a shared
dirty-workspace protocol for that, and never revert or commit around
uncommitted work they haven't attributed.

## Surviving context loss

Long agent sessions get compacted. `onto handoff <change>` emits a compact
recovery context pack — identity, phase, pending gate, artifact excerpts
plus a content hash — and `--write` persists it under the workspace, so a
fresh session resumes without re-deriving state. `onto set build-pause
plan-ready` records a first-class pause at the plan-ready gate for the same
reason.

## Picking the work up cold

The reason onto costs more per change than [`to`](to-workflow.md) is that
someone who was not there has to resume it or check what was done. Four things
carry that, and nothing else claims to:

| Question | What answers it |
|---|---|
| Where is this change, really? | `onto state <name> --json` — `derived_phase` is read from the artifacts, so a stale claim shows up as `phase_mismatch` |
| What do I do next? | `onto handoff <name>` — identity, phase, pending gate, artifact excerpts; the first unchecked `tasks.md` item is the resume point, and its detail is under the matching `## Task N.M` in `plan.md` (`onto doctor` reports any drift between the two) |
| Was this actually decided, or assumed? | the evidence tokens — `proposal-approved`, `approach-confirmed`, `close-confirmed` are recorded answers the binary refuses to advance without, and `notes.md` keeps the user's words |
| Did it really pass? | `verification.md` — every delta-spec scenario with the literal command output, cross-checked against the recorded `verify.result` on leaving verify |

**Who** answered and **when** come from git, not from onto: the state file and
every artifact are committed, so `git log`/`git blame` over
`docs/changes/<name>/` attributes each gate answer and each task's checkoff to
a person and a time. onto deliberately stores no identity of its own — it would
be a second, weaker copy of what the VCS already guarantees. The archived
workspace under `docs/changes/archive/` is that whole record, kept.

## Tooling providers

onto names no tool of its own. You declare which providers the workflow uses
in `[tooling]`, and `homonto apply` renders them into the dispatcher's
generated `references/tooling.md`:

```toml
[tooling]
shell_proxy = "rtk"       # or "none" (default)
code_intel  = "graphify"  # or "okf", or "none" (default)
```

Both keys default to `none`, so a config that declares nothing gets a
preflight that names nothing and grounds by direct file reading. A declared
provider that is missing produces a warning and the workflow proceeds — a
degraded session still works. `homonto doctor` reports the same as a warning.

homonto never installs, updates, or runs a provider; you install it yourself.
See [configuration](configuration.md#tooling) for the full reference.

The principles the skills enforce throughout — build only what the change
needs, as simply as it can be built — are spelled out in [YAGNI](yagni.md)
and [KISS](kiss.md). The lightweight sibling workflow is
[to](to-workflow.md); the two frameworks are an exclusive choice per
repository.

> homonto's own repository is not developed with onto — see
> [`docs/personas.md`](../personas.md). onto is a shipped product framework;
> this guide documents it for projects that adopt it.
