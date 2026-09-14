# The onto workflow

**onto** is a spec-driven development workflow that homonto ships as a
bundled framework. It has two halves that work together:

- the **`onto` binary** (built from `cmd/onto/`, installed beside
  `homonto`) — the deterministic operator that creates change workspaces,
  gates phase transitions, merges spec deltas, archives workspaces, and records
  completed Git integration; and
- the **`onto-*` skills** (materialized from the builtin catalog by
  `homonto apply`) — the agent-facing process prose that drives the work
  inside each phase.

The binary owns the *state and the gates*; the skills own the *work*. A
change moves through five phases in a fixed order:

```
open → design → build → verify → close
```

`close` is the final recorded phase; `onto close` archives the change, and
`onto complete-integration` records a local merge, proven no-op, or opened-PR claim
before the derived phase becomes `done`. Each change
tracks its phase and gate evidence in an `onto-state.yaml` inside its
workspace directory, always written through the binary and never by hand.

This guide covers the concepts. The precise command surface and every gate:
[onto reference](onto-reference.md). Diagnostics at the tool boundary and the
limits of observers: [enforcement](enforcement.md).

## Install and enable

`onto` is version-stamped and installs alongside `homonto`:

```bash
go install github.com/noviopenworks/homonto/cmd/onto@latest
onto version            # prints: onto <version>
```

Or install `onto` together with `homonto` through the [interactive
installer](getting-started.md#1-install): it asks which binaries you want,
verifies the release archives against `SHA256SUMS`, and prints PATH
instructions without editing your shell configuration. On confirmation, it can
also scaffold the current directory with `homonto init`.

The mutating commands (`init`, `new`, `set`, `advance`, `bypass`, `close`, `abandon`,
`merge-deltas`, `complete-integration`) require the onto framework to be **declared and applied
through homonto first**. This is how the skills land in your tools:

```toml
[frameworks.onto]
source = "builtin:onto"
scope = "project"
# plus a [subagents.<name>.<tool>] model block per onto agent — see the
# configuration reference
```

Then `homonto apply`. Inspection commands do not require the framework install
gate, but resolve `homonto.toml` for the configured records root and source
scope when needed. Legacy config-free recovery uses `docs`. `scale --set` and
`handoff --write` explicitly write; the ordinary inspection forms do not.

`homonto apply` also installs the framework's **slash commands** into each
tool: `/onto` (the dispatcher — it derives the active change's real phase
and routes automatically), the command-only explicit-user `/onto-bypass`, plus `/onto-open`, `/onto-design`, `/onto-build`,
`/onto-verify`, `/onto-close`, `/onto-fix`, `/onto-tweak`, and
`/onto-no-slop`. Every workflow command enters the shared `homonto`
coordinator agent (ADR 0045) and loads its matching skill; every state
change still goes through the binary.

## The layout

`onto init` scaffolds four directories under `[workflow].root` (default `docs`),
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

Schema 2 can separate a non-Git config root, records Git, and selected source
checkouts. Run workflow commands with `--dir <config-root>` even from an
execution worktree. `[repos]` declares all available sources; `onto new --repo`
selects the complete change-specific set, with no implicit config source.
Schema 0/1 retains config Git plus selected siblings. See
[workspaces](workspaces.md) for layout, frozen bases, and managed checkpoints.

## Phase walkthrough

The `onto-*` skills carry the process discipline inside each phase; the binary
gates the transitions between them. Review checkpoints carry **evidence
tokens**, a recorded `onto set` summary the binary refuses to advance without:

| Token | Gate it records | Refused without it |
|---|---|---|
| `proposal-approved` | proposal reviewed against request and grounding (full) | `advance` out of open |
| `approach-confirmed` | approach selected with its basis (full) | `advance` design → build |
| `close-confirmed` | close plan validated (all) | `merge-deltas`, `close` |

`onto gate <name> --json` lists whichever are still pending with the exact
resolving command. Leaving verify and closing additionally cross-check
`verification.md`'s `Result:` line against the recorded `verify.result`; the
report and state must agree. Close also requires both Git anchors and validates
`close.merged` against a versioned receipt containing the exact delta manifest
and living-spec pre/post-images. A
full change already in flight when you upgrade refuses its
next `advance` until its missing review is performed and recorded. `onto state
<name> --json` also derives the
**working phase** from the artifacts (`derived_phase`, with
`phase_mismatch` when the claim disagrees), so skills route on tested
derivation instead of re-reading the evidence table by hand.

- **open** — clarify the requirement, decide whether the work should split
  into several changes, and create the workspace (`onto new`).
- **design** — ground-truth exploration, 2–3 candidate approaches, selection
  from evidence and user-owned constraints, then `design.md`, ADR drafts (unnumbered,
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

### Task and trace IDs

New tasks use a dotted plan ID plus a numeric trace marker:

```md
- [ ] 1.1 Implement parser [trace #1]
## Task 1.1 — Implement parser
```

The dotted ID binds `tasks.md` to `plan.md`, which `onto doctor` checks. The
numeric trace ID binds evidence records and graph output: use
`onto evidence record <change> --task 1 ...`. Keep trace IDs positive and
unique within the change. Legacy leading `#1` task lines remain readable, but
new work uses the explicit marker to avoid mistaking an issue reference for a
trace ID.

- **verify** — scale-appropriate check of every delta-spec scenario with
  fresh command output as evidence. Finalize `verification.md` before appending
  structured claims with `onto evidence record`, then record the overall result
  with `onto set verify-result`. A changed report requires re-verification and
  fresh current claims.
  `onto scale` derives the appropriate verification level from the measured
  source diff; explicit mode aggregates selected execution checkouts.
- **close** — `onto merge-deltas` merges the change's delta specs into
   `<workflow-root>/specs/` with a crash-recovery receipt, then `onto close` archives the
  workspace once all evidence gates pass. Number and accept ADRs into
   `<workflow-root>/adr/`, update the affected guides, integrate the pinned
  candidate, and record `merge:<commit>`, proven `unchanged:<commit>`, or
  `pr:<url>` with `onto complete-integration`. Explicit-source PR completion
  requires `--head <canonical-commit>` from the publication tool; this is an
  external assertion, not onto attesting to remote publication. Merge parents
  must bind the exact candidate, allowing only combined-layout records-only
  descendants, not arbitrary later source commits. State keeps immutable diff
  and target anchors per selected repo in explicit mode, or scalar `base_ref`
  and `base_branch` in legacy mode. Prefer allocating any registered receiver
  early, but identity-checked terminal/archive allocation also supports
  post-archive recovery. Optional `--state-id` disambiguates archived generations
  without overriding existing bindings; see [workspaces](workspaces.md#allocate-an-integration-receiver).

Two **presets** run a reduced path for small work: `onto new --workflow fix`
(an existing-behavior bug) and `--workflow tweak` (copy/config/docs-scale
change) go `open-lite → build → verify → close`, skipping design, and
upgrade automatically to design in the full path when scope grows, invalidating
its prior build, verification, and close evidence. A preset reaches build in one
gated call — `onto advance <name> --to build` — instead of two ceremonial
advances; presets are exempt from the two full-only tokens but still record
close-plan validation. `onto abandon` is the
unsuccessful terminal state for work that stops rather than completes.

No-spec fix/tweak changes declare each `Scenario-ID: <id>` once, on a standalone
line in **one canonical location**, either `tasks.md` or `verification.md`, not
both for the same ID. Use plain mentions elsewhere rather than repeating the
declaration or fabricating delta specs. Structured evidence keeps every attempt,
but the last appended claim for each unambiguous `(repo, task, scenario)`
supersedes earlier ones. Duplicate declarations are doctor findings with paths
and line numbers, even without a sidecar; ambiguous IDs provide neither coverage
nor trace supersession. Prose references and fenced examples are not declarations.
See [ADR 0053](../adr/0053-keep-history-without-blocking-fresh-verification.md).
Doctor also checks current claims for staleness. The failed-round counter remains
historical: at least three failures is a doctor finding only while the current
verification result is still `fail`, not after a later pass.

## Specialist subagents

`homonto apply` installs the framework's agents. Do not also declare them in a
top-level `[subagents.*]` table; the names collide.

onto ships **five** agent definitions: the shared `homonto` coordinator plus
four specialists the skills delegate to.

- **`homonto`** — the coordinator, and the one agent that is not a
  specialist. Both workflow frameworks declare it from the same catalog path
  (ADR 0045), so installing onto, to, or the `h` GitHub skill bundle installs
  the same agent; it is declared `primary: true`, which renders as OpenCode's
  `mode: primary`, where the `/onto` command carries `agent: homonto` and
  routes into it. The agent prompt deliberately does not restate the skill,
  so the two cannot drift.

- **`onto-explorer`** — read-only with no shell; reads across many files to
  answer "how does X work / where does behavior live", returning conclusions
  rather than dumps. Used
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
  prompted to **refute, never approve** (ADR 0007). It has no shell, so it
  requests exact probes for the coordinator to run and cannot mutate the
  candidate. That independence is the point.

Planning, judging scope, deciding, and every `onto` binary call stay with the
coordinator. Who does the *editing* depends on the change's
`build_mode` field (`onto set build-mode <change> direct|subagent`): under
`direct` the orchestrator does it, and under `subagent` the implementer edits
and commits its own task's files.

All declare their capabilities once in a tool-neutral `homonto:` frontmatter
block, rendered into OpenCode's `permission:` map (see
[subagents](subagents.md)). Parallelization follows write capability rather
than agent identity
([ADR 0035](../adr/0035-deny-shell-access-to-concurrent-specialists.md)): the three
read-only agents run concurrently wherever the work is independent — grounding
in open and design, per-task and per-lens review in build, scenario evidence
and the skeptic lenses in verify. The edit-capable implementer runs one at a
time unless each has its own git worktree and a disjoint file set, which is
what `isolation: worktree` is for. Questions belong to the orchestrator alone:
a subagent returns `Questions:` or `Evidence requests:`, and the orchestrator
investigates or runs the probe. It asks the user only when product intent
remains unresolved, then re-dispatches. That protocol
is the real guarantee — the rendering backs it in OpenCode, where
`dialogs: false` becomes `question: deny`. `onto gate --json` supplies pending
recorded decisions and safe defaults; it does not make each item a user question.
The orchestrator — your main session — still owns
every edit and commit.

## Working in a dirty tree

Uncommitted work is normal: an interrupted task, a parallel change, your own
edits. onto classifies it rather than treating "dirty" as one condition.
`onto dirt <change> [--json]` audits recorded sources, using registered execution
worktrees when present. Explicit mode selects only the recorded aliases;
legacy mode also includes config Git. An unselected repo or independent records
repository is not an extra source gate. Only a real combined source/records
checkout gets the following three-way classification; all paths in a separate
source checkout are `source`, even if it happens to contain `docs/changes/`:

| Class | What it is | Blocks this change's close? |
|---|---|---|
| `own` | the change's own `<workflow-root>/changes/<name>/` artifacts | **yes** — its evidence must be committed |
| `change` | another change's docs, or the archive | no — that change's own close gate owns it |
| `source` | any other path in the selected execution checkout | **yes** until resolved under the user's preserve/isolate/cleanup choice |

That split lets two changes be in flight at once: one change's half-written
proposal no longer blocks another change's close. When close *is* blocked,
the refusal names the offending paths instead of a bare "dirty worktree".
Invalid aliases, Git identities, or worktree bindings fail closed, not as clean.

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
plan-ready` records an explicitly requested pause after planning; ordinary builds
do not set it.

## Picking the work up cold

The reason onto costs more per change than [`to`](to-workflow.md) is that
someone who was not there has to resume it or check what was done. Four things
carry that, and nothing else claims to:

| Question | What answers it |
|---|---|
| Where is this change, really? | `onto state <name> --json` — `derived_phase` is read from the artifacts, so a stale claim shows up as `phase_mismatch` |
| What do I do next? | `onto handoff <name>` — identity, phase, pending gate, artifact excerpts; the first unchecked `tasks.md` item is the resume point, and its detail is under the matching `## Task N.M` in `plan.md` (`onto doctor` reports any drift between the two) |
| Was this actually decided, or assumed? | the evidence tokens — `proposal-approved`, `approach-confirmed`, `close-confirmed` are recorded reviews the binary refuses to advance without, and `notes.md` keeps their basis plus the user's words when supplied |
| Did it really pass? | `verification.md` — every delta-spec scenario with the literal command output, cross-checked against the recorded `verify.result` on leaving verify |

**Who** answered and **when** come from git, not from onto: the state file and
every artifact are committed, so `git log`/`git blame` over
`<workflow-root>/changes/<name>/` attributes each gate answer and each task's checkoff to
a person and a time. onto deliberately stores no identity of its own — it would
be a second, weaker copy of what the VCS already guarantees. The archived
workspace under `<workflow-root>/changes/archive/` is that whole record, kept.
In managed mode, binary mutations checkpoint operation-scoped records; manual
Markdown needs an explicit path-scoped checkpoint. Git identifies the commit
author, not every editor whose changes share a file. See
[records history](workspaces.md#records-history-and-source-commits) for recovery
and that attribution limit.

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
[to](to-workflow.md); the two frameworks are complementary — declare both
and pick per change; the shared `homonto` coordinator loads whichever
dispatcher the request names.

> homonto's own repository is not developed with onto — see
> [`docs/personas.md`](../personas.md). onto is a shipped product framework;
> this guide documents it for projects that adopt it.
