# `to` — design document

Status: implemented — the `to` binary (`cmd/to/`, `internal/tocli`,
`internal/tostate`), the `builtin:to` catalog framework, and the onto-xor-to
config validation ship as described below.
Origin: interview-driven design session (2026-07-18). Each decision below was
made explicitly; the two marked **overruled** went against the interviewer's
recommendation and carry recorded consequences.

## What `to` is

`to` is a **minimal coding framework for LLMs**: the smallest structure that
still makes agent-written code good. It gives an agent a shape to work in —
`plan → do → done` — and holds the code produced inside that shape to onto's
standards. It is the deliberately small sibling of onto — same family
(separate binary + catalog framework, binary-owned state, skills carrying the
process prose), strictly less machinery.

`to` is **not** an enforcement system, and it is not a task tracker with
ambitions. It has no evidence gates, no spec deltas, no dependency graph, no
dirt classification. Its value is the working shape, the reviewable `plan.md`
artifacts, trustworthy cross-session status — and above all the coding
discipline its skills impose on the LLM (see
[Quality carried over](#quality-carried-over-from-onto)).

## onto and `to` are an exclusive choice

A repository uses **one** workflow framework, never both:

- **`to`** — simple development. Solo or small-team repos, straightforward
  changes, minimal ceremony per change. The record it leaves is one `plan.md`
  and the git history.
- **onto** — work that is **handed off or audited**. Someone who was not there
  has to resume the change or check what was done, so onto pays for an
  archived workspace, recorded gate answers, spec deltas, and evidence-gated
  transitions. That is the axis, not company size.

Declaring both `[frameworks.onto]` and `[frameworks.to]` in one `homonto.toml`
is a **config validation error**. The exclusivity is also structural: the two
tools share no state format and no directory territory (`to` owns
`docs/tasks/`, onto owns `docs/changes/`), so neither tool's commands can
misread the other's artifacts.

There is **no escalation path**. A `to` change cannot be promoted into an onto
change (`onto adopt` is a non-goal); the documented answer is "redo it as an
onto change by hand." Pick the framework per repository, not per change.

## Decisions

### Shape: `plan → do → done`

Three phases, linear-forward advancement, `abandon` as the only other exit.
`done` archives the change directory to `docs/tasks/archive/`.

### The gate is a checkbox, and we own that *(overruled)*

`to done --verified` is self-asserted — a flag the agent sets, not evidence
the binary observes. The design interview surfaced that this makes any "hard
gate" claim vacuous, and the decision was to own the reframe: the binary is
bookkeeping, not a guarantee — `to`'s rigor lives in the skills, not the gate.
Consequences:

- Docs and skills must never imply the checkbox is a guarantee.
- Real verification rigor lives in the `/to-do` and `/to-done` skill prose
  (run the tests, record the outcome in `plan.md`), not in the binary.
- The optional `--evidence "<text>"` flag records *what* was asserted,
  verbatim and unchecked, in the archived state — added post-v0.4.0 so a real
  verification is distinguishable from a skipped one after the fact. It adds
  no ceremony (optional) and no guarantee (never checked).

### Layout: `docs/tasks/<name>/`

Each change is a small directory: a state YAML written **only by the binary**,
plus a `plan.md` the agent writes during `plan`. Per-change directories keep
parallel changes conflict-free; plans stay reviewable in PRs. Fully disjoint
from onto's `docs/changes/`.

### State format: hard wall from onto

`to`'s state schema is designed independently — no field-name compatibility,
no shared code paths with `ontostate`. Simplest possible schema wins.

### The binary: deterministic bookkeeper + handoff

`cmd/to/`, the module's third binary. It exists because hand-maintained
markdown state decays the moment two sessions touch one change; the binary is
the sole writer of state, so `to status` stays trustworthy across context
compactions, sessions, and agents.

Surface (`init`, `new`, `status`, `phase`, `done`, `abandon`, and `handoff`
support `--json`; `doctor` uses `--quiet` for hook-friendly exit-code-only
checks, and `version` prints plain text):

| Command | Role |
|---|---|
| `to init` | Scaffold `docs/tasks/` in a repo. |
| `to new <name> [--repo <declared-name>]` | Create a change: state YAML + empty `plan.md`. Selected repo names are recorded in the config repo; only an active change blocks a name. |
| `to status` | All active changes and their phases. Read-only, config-independent. |
| `to phase <name>` | Advance the change one phase forward. |
| `to done <name> --verified [--evidence "<text>"]` | Mark done and archive to `docs/tasks/archive/<date>-<name>/`. A scoped change also requires clean, determinable Git state for the config repo and every selected declared repo. |
| `to abandon <name>` | Terminal exit without done. |
| `to handoff <name>` | Compact context-recovery pack (phase, plan head + complete unchecked task contracts, final check, bounded notes, safe next skill) for resuming after compaction. Read-only, config-independent. |
| `to doctor [--quiet]` | Workspace health: invalid state, wedged terminal-but-active changes, missing plans, incomplete `Files:`/`Change:`/`Verify:` task contracts or `Final Verify:`, and version skew. Findings are diagnostic, not phase gates. `--quiet` is exit-code-only — the enforcement hook primitive. Read-only, config-independent. |
| `to version` | Print the release-stamped binary version as plain text. |

Crash safety: `done`/`abandon` write terminal state then archive; re-running
the same command converges an interrupted finish, and doctor reports it.
Change-mutating commands (`new`, `phase`, `done`, `abandon`) take a fail-fast
workspace lock so concurrent sessions cannot interleave writes. `init` only
creates the fixed task directories with idempotent `mkdir` operations and does
not take the lock.

### Scoped Git Gate

Unscoped changes remain Git-blind. A change created with one or more
`--repo <declared-name>` aliases records that scope in `to-state.yaml`; the
config repo is implicit. `to done` then fails closed unless every selected
worktree is clean and Git can inspect it. The record stores aliases, not paths
or Git facts, so current `[repos]` remains authoritative and facts do not rot.

### Skills: dispatcher + three phase skills + quality skills

Catalog framework `builtin:to`:

- `/to` — dispatcher: runs `to status --json`, derives the active change's
  phase, routes to the matching phase skill.
- `/to-plan` — write `plan.md`; suggests (does not require) a branch.
- `/to-do` — the code-writing skill: carries the code-writing standards
  (below) and orchestrates the implementer/reviewer subagents (below): one
  implementer at a time, reviewers concurrently.
- `/to-done` — verify for real, obtain at least one completed skeptic pass on
  the final candidate, record the outcome, `to done --verified`, archive.
- `/to-no-slop` — vendored no-slop prose skill (below).

### Subagents: onto's cast, adapted — one writer at a time

`to` vendors onto's four specialist subagents, renamed `to-*` and modified to
match the `to` philosophy. The division of labor survives: a cheap worker
edits, a judge reviews, neither plans.

**Concurrency follows write-scope, not agent identity** (ADR 0019). Three of
the four never edit, so they cannot corrupt a shared tree and run concurrently
— several explorers on different questions, several reviewers or skeptics
applying different lenses. `to-implementer` runs strictly one at a time: it is
the only writer and `to` keeps a single working tree. Parallel implementers
need a worktree each, which is onto's machinery and deliberately out of scope
here.

*Superseded:* this section originally read "no parallelization — `to` never
runs subagents in parallel." That rule was broader than its own justification,
which was only ever about the shared working tree and `plan.md`.

| Subagent | From | Role in `to` |
|---|---|---|
| `to-explorer` | `onto-explorer` | Read-only codebase questions during `plan` and `do`; returns conclusions, not dumps. Unchanged apart from naming. |
| `to-implementer` | `onto-implementer` | Executes one bite-sized task from the plan: edits, runs that task's verification, returns a diff summary. Never plans, never spawns. |
| `to-reviewer` | `onto-reviewer` | Reviews the implementer's diff for correctness, security, and clarity; read-only plus git inspection; findings ranked by severity. |
| `to-skeptic` | `onto-skeptic` | Read-only, so `to` may run several concurrently, one per lens; **at least one completed pass on the final candidate** is required in `/to-done`. A verdict invalidated by later code changes is rerun against the new candidate; only verdicts for the archived tree are kept. |

The `/to-do` loop is: pick the next plan task → `to-implementer` writes it →
`to-reviewer` judges the diff → orchestrator applies accepted findings (via
the implementer again if substantial) → next task. Review findings are acted
on or explicitly declined with a reason in `plan.md`'s `## Notes` section —
never silently dropped.

### Bootstrap: same gating as onto *(overruled)*

Mutating commands (`init`, `new`, `phase`, `done`, `abandon`) refuse until
`[frameworks.to]` is declared in `homonto.toml` and `homonto apply` has run.
Read-only commands (`status`, `handoff`, `doctor`, `version`) work anywhere,
config-free.

This re-imports onto's setup ceremony into a tool branded "much less hassle,"
and that tension is accepted deliberately: with no gates, **the skills are the
product**, and gating guarantees no agent ever works inside the framework
without the coding discipline that gives it meaning. The honest tagline is
therefore *"much less hassle per change,"* not "zero setup."

### Name: `to`

The onto/`to` pairing tells the product story in four letters — same family,
strictly less. The searchability cost of a two-letter English word is accepted;
mitigation by convention: always write it backticked (`to`) or as "the `to`
binary" in docs.

## Quality carried over from onto

`to` cuts onto's *machinery*, not its *standards*. The flow is simple, but the
code written inside it is held to the same bar:

- **No-slop prose.** The framework vendors onto's `onto-no-slop` skill (itself
  a build of Hardik Pandya's stop-slop) as `to-no-slop`. All prose artifacts a
  `to` change produces — the plan and its notes, verification record, and
  commit messages — go through it. `to` cannot reference onto's copy because
  the two frameworks are never installed together, so it ships its own.
- **Code-writing standards.** The `/to-do` skill carries the code-quality
  prose adapted from onto's build phase: read the surrounding code before
  changing it, match its idiom and comment density, keep changes focused, add
  or update focused tests for behavior changes, and run the narrowest useful
  verification before claiming done.
- **Division of labor.** onto's implementer/reviewer/explorer/skeptic
  subagents come along (as `to-*`), so code is still written from bite-sized
  specs and judged by a fresh context before it lands — with one writer at a
  time and read-only agents run concurrently (see
  [Subagents](#subagents-ontos-cast-adapted--one-writer-at-a-time)).

In one line: **simple flow, but a code-writing discipline** — the phases got
lighter; the code and prose that come out of them did not.

## Recorded risk

The two overruled decisions compound: a gate-less framework behind a mandatory
homonto setup means the user pays entry cost for a tool whose binary
guarantees nothing. `to` stands or falls on its skills being genuinely good
coding discipline for LLMs — that is where the engineering effort must go.
