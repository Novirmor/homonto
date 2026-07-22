---
name: onto
description: onto workflow dispatcher. Use when starting, resuming, or asking about any development work in a repo with the docs/ onto layout — runs tooling preflight, finds the active change, derives the real phase from file state, and routes to the matching onto sub-skill.
---

# onto — Workflow Dispatcher

onto is a five-phase development workflow — **open → design → build → verify →
close** — plus two preset paths (`onto-fix` for bugs, `onto-tweak` for small
non-bug changes). All artifacts live in one `docs/` tree. **Every state
mutation goes through the `onto` binary** (`onto new`, `onto set …`, `onto
advance`, `onto close`): it is the single authority for `onto-state.yaml` and a
hard dependency of these skills — the tooling preflight below fails loudly if it
is missing. The skills never hand-edit the state file. Phase is always
cross-checked against real file state: the state file is a cache of truth, not
truth.

The dispatcher does exactly four things, in order: preflight → discover →
derive → route. It never performs phase work itself.

## 1. Tooling preflight (runs first, every dispatch — warns, never halts)

Run these checks before anything else. A missing tool produces a WARNING
and the workflow proceeds — degraded is still working; the warning tells
the user what they are missing and how to fix it.

0. **onto binary** (required — this is the one hard dependency). Run `onto
   version`. On failure, STOP: the skills drive all workflow state through the
   `onto` binary; without it no phase can mutate state safely. Tell the user to
   install/build it (`go build ./cmd/onto`) before proceeding. This is the only
   preflight check that halts; `rtk` and `graphify` below still warn-never-halt.

1. **rtk** — run `rtk --version`. On success, all subsequent shell
   operations in every onto phase go through rtk (or the rtk hook rewrites
   them transparently). On failure, WARN and proceed: rtk (token-optimized
   CLI proxy) was not found on PATH — token costs will be higher; install
   rtk to reduce them.

2. **graphify** — confirm codebase-understanding tooling is available: the
   `graphify` skill is loadable, or a `graphify-out/` directory or
   `.codegraph/` index exists at the repo root. The open and design phases
   ground every codebase claim in graphify/codegraph queries when they are
   available, rather than guesswork. Indexing is the user's decision: if
   only the skill is available and no index exists, ask the user whether to
   build one before open/design proceeds; if they decline, grounding falls
   back to direct file reading and that fallback is recorded in the
   proposal/design.
   **Staleness counts as absence**: an index older than the recent work
   (rule of thumb: predates the last ~20 commits or is weeks old) gets the
   same ask-to-reindex treatment — ask or proceed, never a halt — and the
   Grounding section records the index's age either way — a confidently
   stale graph is worse than none. If neither the skill nor an index
   exists, WARN, record `grounding: direct file reading (graphify
   unavailable)` in the change's notes.md Grounding section, and proceed.

## 2. Active-change discovery

Scan `docs/changes/*/` excluding `archive/`. A change is active iff its
directory sits directly under `docs/changes/` **and holds a `proposal.md`
or an `onto-state.yaml`**, with `onto-state.yaml` (when present) reading
`archived: false`. A directory with neither artifact is not a change —
skip it (a `templates/`, a scratch dir, an editor folder is not a phantom
active change; never rebuild an onto-state.yaml into it). Also sweep
`docs/changes/archive/*/onto-state.yaml` for `archived: false`: that is a
close interrupted between the `git mv` and the flag — surface it and
finish the interrupted archive (the moved workspace still needs its
`archived: true` flag the halted `onto close` never wrote).
If a change carries an `abandoned: true` flag it is retired — never list it
as active.

While discovering, also check the git tree: if `onto dirt <active-change>
--json` reports anything, follow `references/dirty-workspace.md` **before**
executing the routed phase — the binary classifies (`own`/`change`/`source`,
and what blocks close); you attribute `source` dirt to an owner. Never build
on top of, revert, or commit-around uncommitted work you haven't attributed.

| Active changes | User input | Behavior |
|---|---|---|
| None | description given | Route to `onto-open` with the description |
| None | nothing | Ask what the user wants to work on, then `onto-open` |
| Exactly one | nothing | Resume it: derive phase, route |
| Exactly one | new description | ASK: continue the active change or open a new one |
| Two or more | anything | LIST them (name, workflow, claimed phase, deps status) and ASK which to resume before doing anything else |

**Dependencies**: each change's `onto-state.yaml` may name `deps:` — changes
that must archive before this one builds. A dep counts as **archived iff
a directory `docs/changes/archive/????-??-??-<dep>/` exists** — the
date-anchored exact-name match (`YYYY-MM-DD-` prefix per the archive
contract), never a bare suffix match, which falsely resolves deps whose
name is the tail of another change's name. **An active workspace with the
dep's name overrides any archive hit** (a reused name in flight is not
archived). Discovery listings show deps status (`ready` /
`blocked by <name>`). Before resuming a change whose deps are not all
archived, warn and require an explicit user choice: proceed anyway,
switch to the dependency, or stop. Two findings to surface immediately:
a dep matching **no active and no archived change** (ask the user to
correct or drop it), and a dep chain that **reaches the current change —
including a self-dep or an A⇄B cycle** (unsatisfiable by construction;
ask the user to break the cycle). For multiple simultaneously active
changes, recommend one git worktree per change — coupled work that can't
be separated should have been one change (the split-preflight rule
already says so). **Close them one at a time**, though: two closes running
at once both merge into shared `docs/specs/*` and both draw ADR numbers
from the same `docs/adr/` (onto-close re-scans before each move to avoid a
clobber, but serial closes remove the race outright).

If the repo has no `docs/changes/` tree at all, offer to bootstrap the
layout: create `docs/{adr,specs,changes/archive,guides}/`, writing
`docs/changes/README.md` from `references/changes-readme.md` and
`docs/specs/README.md` from `onto-close/references/specs-readme.md` (the
`docs/adr/` numbering contract and `docs/guides/` are conventional). Then
proceed to `onto-open`.

## 3. Phase derivation and cross-check

`onto-state.yaml` is the binary's record of the workflow state. On every
dispatch:

1. Read `onto-state.yaml`. Its canonical schema, template, and per-field
   meaning live in `references/state-yaml.md` in this skill's directory —
   **the single source**. `docs/changes/README.md`, when the repo has one,
   points here rather than copying, so the two never drift. If a skill's
   `references/` directory is genuinely missing, say so, fall back to
   reading this SKILL.md's own tables, and continue — degrade, never halt;
   but note that a reconstructed lint or grammar is weaker than the real
   one, so flag any close run made without them.
2. Independently derive the phase from artifacts with this table
   (**first match from the top wins — strongest evidence first**; this
   table is authoritative — any repo README points here, never re-states
   it):

| Evidence | Real phase |
|---|---|
| `archived: true` or workspace under `archive/` | done |
| `design.md` marked `Status: Under revision` | design |
| `verification.md` with a `Result: pass` line | close |
| `tasks.md` contains ≥1 task and all are checked | verify |
| `design.md` marked `Status: Confirmed`, or a preset workspace | build |
| full workflow: a `tasks.md` (≥1 task, not all checked) or a `design.md` draft present, not yet `Status: Confirmed` | design |
| full workflow: `proposal.md` present, no `tasks.md`/`design.md` yet | the claimed phase (open **or** design — see the open↔design note in §3) |
| `proposal.md` missing / workspace incomplete | open |

Note: for a **full** change, `tasks.md` and `design.md` are *design* deliverables
— open now produces only `proposal.md` (the task list is derived from the
confirmed design). So the presence of `tasks.md` is design evidence, not open
evidence, and a full change with only `proposal.md` is not file-distinguishable
between open and design.

3. **Files win downward; gates win upward.** If the derived phase is
   earlier than the claimed phase, route at the derived phase and surface
   the mismatch to the user — **never hand-edit `onto-state.yaml` to
   demote it**. The binary owns that file; a backward phase move is not a
   binary operation (the workflow has no `onto reopen`). Record the
   discrepancy in `notes.md` and let the user decide: redo the work and
   re-advance, or `onto abandon` if the change is dead. If the derived
   phase is later than the claimed phase, do not silently promote — the
   phase field advances only when a phase's exit gate is answered, so a
   lagging claim means an unanswered gate: resume at the claimed phase's
   gate (artifacts already prepared) and let it advance normally.
   **One exception: the verify→close boundary has no gate** (the
   failure gate fires only on a fail). So a `phase: verify` claim beside a
   `verification.md` reading `Result: pass` is not an unanswered gate — it
   is a lagging write. Advance `phase` to `close` via `onto advance` and
   route to `onto-close` without re-verifying; the pass already stands in
   the file. Re-running verify here would only discard fresh evidence the
   report already holds. **The open↔design boundary also has no
   distinguishing file signal for a full change** — `tasks.md`/`design.md`
   are design deliverables, so a `phase: design` claim with only
   `proposal.md` present is a design phase whose work hasn't landed yet,
   NOT a demote-to-open. Trust the claimed phase across open↔design;
   demote to open only when `proposal.md` itself is missing (a genuinely
   incomplete workspace).
4. **Cross-check `workflow` too, not just phase.** Resolve it in this
   priority order and stop at the first that applies:
   1. The proposal's `Preset:` marker. An upgrade annotation
      (`Preset: fix (upgraded to full YYYY-MM-DD)`) means **full**.
   2. A `Status: Confirmed` (or `Under revision`) `design.md` means
      **full** — a designed change has a lifecycle no branch name can
      strip, so the branch prefix is ignored here.
   3. The branch prefix (`fix/`, `tweak/`) — only when neither 1 nor 2
      applies (the branch belongs to the checkout, not the change; a
      leftover `fix/` branch must not demote a real change).
   4. Otherwise **full** (a detached HEAD or non-prefixed branch is no
      signal).

   On mismatch the file sources win — surface the mismatch, reroute —
   with one hard asymmetry: **an upgrade (preset→full) may be recorded
   silently, but a downgrade (full→preset) never happens without fresh
   user confirmation.** Never talk a change down. To record an upgrade,
   annotate the proposal's `Preset:` line (the dispatcher re-derives
   `workflow: full` from it); there is no `onto set workflow` command.
5. A missing or malformed `onto-state.yaml` is a recovery situation, never
   a silent rewrite: surface it to the user, reconstruct the *routing*
   from the file-evidence table above, and record the recovery in
   `notes.md`. **Do not hand-write a replacement `onto-state.yaml`** — the
   binary is its sole authority. If the file is genuinely lost, the
   honest path is `onto abandon <name>` followed by a fresh `onto new`
   that re-creates the canonical state, with the recovered routing
   deciding where work resumes. Cap the resumed phase per the boundary
   table in `references/state-yaml.md` so a lost state file does not skip
   what the user never confirmed.
6. Never trust conversation history for phase detection — after context
   loss or compaction, this derivation is the recovery mechanism. Re-run it.
   The derivation recovers the *phase*; for the *content* (what the change is
   about, the pending decision, the artifacts), run **`onto handoff <change>`** —
   a compact recovery pack (`--write` persists it under the workspace). Write one
   at a risky boundary (before a long build, before an expected compaction).

## 4. Routing table

| Derived state | Load skill |
|---|---|
| `workflow: fix` (any phase) | `onto-fix` — presets own their whole lifecycle |
| `workflow: tweak` (any phase) | `onto-tweak` — presets own their whole lifecycle |
| phase open | `onto-open` |
| phase design | `onto-design` |
| phase build | `onto-build` |
| phase verify | `onto-verify` |
| phase close | `onto-close` |
| done | Report that the change is archived; ask what's next |

New work routes by intent: bug fix with clear reproduction → `onto-fix`;
copy/config/docs/prompt touch-up or a small feature within tweak limits
(≤5 files excluding tests, no new capability, no existing-spec requirement
change) → `onto-tweak`; anything needing design → `onto-open` (full).
Preset skills contain upgrade rules that force the full path when scope
grows — never talk a change *down* from full to a preset.

**Reopen and abandon** (both need explicit user intent):

- **Reopen** — a defect found after verify passed but *before* archive:
  route to build. Add tasks for the fix in `tasks.md` and run `onto set
  verify-result <name> pending`; flip `verification.md`'s `Result:` line to
  `Result: superseded (reopened <date>)`. The unchecked tasks plus the
  invalidated result drive the dispatcher's derivation back to build — no
  phase field is written (the binary has no reopen/backward-phase command;
  resume at build and re-advance when the fix lands). A defect in an
  *archived* change is new work — open a fresh `fix` change whose proposal
  references the archived one; archives are never edited.
- **Abandon** — the user drops a change. Run **`onto abandon <name>`**: it
  marks the change `abandoned: true` (the unsuccessful terminal state) and
  saves `onto-state.yaml` in place. The workspace stays under
  `docs/changes/<name>/` — abandon is a state flag, not a move. Discovery
  skips abandoned changes (they leave the active list and never route
  again). No spec merge, no ADR numbering — an abandoned change's deltas
  are never merged into the living specs, and `onto close` refuses to
  archive it as a success. The binary owns the `abandoned` flag; never
  hand-edit `onto-state.yaml` to set it. If you later want the workspace
  out of the active tree entirely, `git mv` it under `archive/` by hand
  and record the move — but the `abandoned: true` flag (not the location)
  is what retires it.

## 5. GitHub entry points (contract)

- **Issue intake** (e.g. a resolve-issue skill): the issue text seeds
  `onto-open` clarification — fix preset for bugs, full workflow for
  features; prefer worktree isolation since intake usually starts from a
  clean default branch.
- **PR-feedback intake** (e.g. a continue-pr skill): review feedback resumes
  the matching change's build phase; if the change is already archived, open
  a new `fix` change whose proposal references the PR.
- PR creation and PR review are NOT part of onto. The workflow ends at a
  verified, closed change on a branch; hand off to the dedicated PR skills
  from there.

## 6. Exit

After routing, the dispatcher is done — the sub-skill owns the phase,
including its gates and exit checklist. Never execute phase work here.

## 7. Delegation, parallelization, and dialogs

The onto framework ships four **specialist subagents** — they install with onto
and the phases delegate to them. They run as independent agents, so several run
**in parallel**. Both tools support this: **OpenCode** dispatches subagents as
child sessions and **Claude Code** runs them as parallel Task-tool agents (send
multiple Task calls in one turn). Each carries an enforced capability profile
(homonto renders it per tool): only the implementer may edit.

**Match the task to the agent — this table is the mapping** (each agent's
model is installer config, `[subagents.<name>.<tool>]` — not workflow
doctrine):

| Task in hand | Dispatch | Capabilities |
|---|---|---|
| Understand something, or locate where behavior lives | `onto-explorer` | read-only, keeps bash (grounding CLIs), no spawn |
| Execute one bite-sized task from a precise spec | `onto-implementer` | **edits**, bash, no spawn |
| Audit a diff for correctness/security/contract/clarity | `onto-reviewer` | read-only, keeps bash, no spawn |
| Refute a verification claim, or hunt what the scenarios miss | `onto-skeptic` **×2, parallel** | read-only, keeps bash, no spawn |
| **Plan, judge scope, decide, commit** | **nobody — you do it** | — |

That last row is the rule the others serve: the orchestrator (this session)
plans, judges scope, and decides, because those steps are gated on user
confirmation and a subagent cannot ask. The **implementer** does mechanical edits
from a handed spec; the read-only specialists investigate, audit, and attack. The
orchestrator owns every commit and every `onto` binary call.

`onto-skeptic` is dispatched **twice at once**, one per lens — `conformance`
(refute each scenario's evidence) and `robustness` (attack the gaps the scenarios
never cover). Name the lens in the dispatch; it attacks only that lens. Both are
prompted to refute, never approve — see
[`onto-verify`'s adversarial protocol](../onto-verify/references/adversarial.md).

> **Subagents never prompt the user.** A subagent needing a decision **returns**
> it as a `Questions:` section; the orchestrator asks the user (via a dialog) and
> re-dispatches with the answer. This is a hard protocol because a **Claude** Task
> subagent *cannot* prompt mid-run (AskUserQuestion belongs to the main session);
> OpenCode subagents can but must not either, so the flow is identical everywhere.

**Delegate, and fan out.** When a phase needs investigation or review, hand it to
the subagent rather than doing it inline — and when the questions are
**independent**, dispatch them **concurrently** (one subagent invocation per
question) instead of serially:

- **design / grounding** — split a broad "how does this subsystem work" into
  several targeted `onto-explorer` tasks and run them at once; synthesize the
  returns into the design.
- **build** — after each task's edits, hand the diff to `onto-reviewer`.
  Independent tasks that touch **non-overlapping** files can be explored/reviewed
  in parallel; tasks that share files stay serial (one commit each, in order).
- **verify** — delegate the change-wide diff audit to `onto-reviewer` while you
  check spec scenarios.

The orchestrator (this session) still owns every edit, commit, and the `onto`
binary calls — the subagents only read and report. Never let a subagent mutate
workflow state.

**Dialogs — prefer them, in either tool.** Whenever a `> **GATE:**` block or any
either/or decision comes up, ask it through an **interactive choice dialog** — a
clear prompt, a short header, and the concrete choices — rather than burying the
question in prose. It is faster for the user and records a definite answer.

For the **recorded evidence gates** (isolation, build/tdd mode, verify result,
close-merged, guides, integration), the binary owns the schema: run **`onto gate
<change> --json`** to get the exact pending decision(s) — id, question, header,
options (with a recommended default), and the `onto set …` command that records
the answer. Render that as the dialog, then run the returned `onto set`. This
keeps the same gate asked the same way in both tools and lets you check what is
pending. Both tools have a dialog mechanism, so use it in both:

- **OpenCode** — the **question** tool (the shipped subagents allow it via
  `permission.question`).
- **Claude Code** — the **AskUserQuestion** tool.

Fall back to a plain written question only when neither is available. A dialog
never *replaces* a gate — it is how the gate is asked.

## Gates are sacred

Every sub-skill contains `> **GATE:**` blocks — blocking user decisions.
A gate may only be skipped when the user explicitly pre-answered *that same
question*; a blanket directive (e.g. "run to completion") pre-answers only
the gates that say so, and must be recorded verbatim via `onto set directive
<name> "<text>"`. When in doubt, stop and ask.

## Prose discipline (every artifact)

onto writes prose a human reads later: `proposal.md`, `design.md`, `notes.md`,
ADR drafts, `verification.md`, guide updates, and commit messages. Run the
**onto-no-slop** skill (bundled with this framework) over each prose artifact
before its phase gate — cut filler and adverbs, use active voice, name the
actor, be specific, vary the rhythm, no em dashes. Record the pass in
`notes.md` (`no-slop: <artifact> done`). There is no numeric self-score — a
model grading its own prose against a threshold is decoration; the edit pass
itself is the control.

It edits prose, never contract. Machine-read markers (`Status:`, `Result:`,
`Preset:`, checkbox syntax, `SHALL`/`MUST` lines, GIVEN/WHEN/THEN), a
requirement's normative wording, and mandated template structure are off-limits
— rewording one breaks derivation or the lint. Keep load-bearing terms and
genuine distinctions; drop the empty adverb and the manufactured reversal. Each
phase's exit checklist re-states this.
