---
name: onto-build
description: onto phase 3 — plan and build. Use when an active change has phase build — writes the implementation plan, pauses at the plan-ready gate, then executes bite-sized tasks with one commit each under the chosen TDD/direct mode.
---

# onto-build — Phase 3: Plan and Build

Turn the confirmed design into a plan, then the plan into committed code —
one small, verified task at a time.

## Entry check

- `onto-state.yaml` has `phase: build`.
- `workflow: full` → a `design.md` marked `Status: Confirmed` must exist; if
  it doesn't, the design phase isn't done — route back through `/onto`.
- Presets (`fix`/`tweak`) enter build directly after open-lite.
- Read `notes.md` at entry when present — recorded decisions and
  directives govern how tasks execute.
- **Resume from a pause**: if `onto state <name> --json` shows
  `build_pause: plan-ready`, `plan.md` is already written — do NOT re-run the
  planning step. Tell the user the build is stopped at plan-ready; once they
  confirm continuing, `onto set build-pause <name> clear`, choose the execution
  config, and proceed.
- On resume (fresh session, context loss): run `onto dirt <name> --json`
  FIRST (falling back to `git status` on an old binary). Dirt classified
  `source` or `own` is usually an interrupted task's partial work —
  reconcile it before continuing per the dispatcher's
  `onto/references/dirty-workspace.md`: reset it, or fold it into the
  unchecked task explicitly (state which in the task's commit); dirt
  classified `change` belongs to another change — leave it. Never build on
  top of partial edits unknowingly — the same rule the subagent protocol
  enforces for fresh agents. Then find the first unchecked task in
  `tasks.md`/`plan.md` and continue from there; never redo committed tasks.

## Steps

### 1. Write the plan

Write `docs/changes/<name>/plan.md` from the canonical template
`references/plan.md`: one `## Task N.M` detail block per `tasks.md` item,
**numbered to match it**, each with exact file paths, what to do, and how to
verify it; mark tasks warranting review `(risk: high)`. A task that can't
state its verification isn't ready. One reviewable commit (~200 lines) per
task — split anything bigger. Read `notes.md` first if present.

`tasks.md` owns completion state; `plan.md` owns the detail. Every item must
have its task and every task its item — a number in one file and not the
other is drift, and the close lint checks for it.

### 2. Plan-ready gate

> **GATE (plan-ready + execution config):** pause. The user reviews the plan
> and chooses the execution configuration, recorded through the binary:
>
> - `onto set build-mode <name> direct|subagent` — direct in-session; subagent
>   only when real background dispatch capability exists
> - `onto set tdd-mode <name> tdd|direct` — tdd for anything with testable
>   logic; direct for content/docs deliverables
>
> Isolation is NOT asked here — it was chosen at the design → build gate (see
> `onto-design`), and the binary already refused the advance without it. If
> isolation is somehow unset at this point (e.g. an older change created before
> the gate moved), ask it now via `onto set isolation <name> branch|worktree`
> before proceeding — build work must never run unisolated.
>
> **Pausing here is first-class.** If the user wants to stop after the plan
> (e.g. to review it later or switch models/sessions), run `onto set build-pause
> <name> plan-ready` and end the invocation — do not choose the execution config
> or start executing. On the next dispatch the plan-ready pause is recorded state,
> so a fresh session resumes cleanly (see Entry check) rather than re-planning.
> Clear it with `onto set build-pause <name> clear` when resuming to execute.
>
> This gate MAY be pre-authorized: if the user gave an explicit directive
> (e.g. "run to completion with defaults"), record it **verbatim** via `onto
> set directive <name> "<text>"` and proceed with the recorded config — but
> still surface the plan summary so the user sees what will happen.
>
> **What qualifies as a directive**: an explicit, unprompted instruction
> covering future gates. Acquiescence is not one — "go ahead", "sounds
> good", "yes" answer only the question just asked. When in doubt it is
> not a directive; ask. A directive authorizes only what it names: "run
> to completion" covers this gate and later gates that say MAY be
> pre-authorized, **except** the close phase's archive gate, which needs
> the directive to cover closing/archiving explicitly (see onto-close).
>
> Record the gate's answer (chosen config, or the pre-authorizing
> directive) in `notes.md` Confirmed as well — the state-rebuild gate cap
> for the build→verify boundary consults notes.md, not the losable
> state file.

Create the isolation before the first task (for `isolation: worktree`, follow
`references/worktree-protocol.md` — creation, env/untracked-file copying, clean
baseline, and teardown) — but check the tree first:
run `git status`. The workspace docs should already be committed (each
phase commits at exit); if they aren't, commit them now. Unrelated
uncommitted changes either get stashed (say so) or force
`isolation: worktree` — never carry a stranger's dirty state onto the
change branch silently. Then `git checkout -b <type>/YYYYMMDD/<change-name>`
(or the worktree equivalent). Type prefix: `feature` for full,
`fix`/`tweak` for presets; an upgraded preset keeps its original branch
(the proposal's upgrade annotation records the lifecycle, not the branch
name).

### 3. Execute task by task

**`execution: subagent`** → follow `references/subagent-protocol.md`: the
main session coordinates only — one fresh-context implementer agent per
task, coordinator verifies commits and checkoffs against the repository
(never the agent's report), reviewer agent after `(risk: high)` tasks and
the final task. If no real dispatch capability exists, fall back to
`execution: direct`, record it, announce it.

> **Parallel implementers are a supported option, not a theoretical one.**
> When the next tasks touch **disjoint file sets** and `isolation` is
> `worktree`, run their implementers **at the same time** — one worktree per
> implementer, coordinator merging in plan order and doing every checkoff
> itself, serially, after the joins. This is onto's biggest available
> speedup on a wide change and the reason worktree isolation exists.
> It is also the one place a subagent can corrupt the tree, so it is
> conditional: `references/subagent-protocol.md` carries the five conditions,
> and `references/worktree-protocol.md` the mechanics. Meet every condition
> or stay serial — the shared-file race is silent.

**`execution: direct`** → for each task, in order:

1. **`tdd: tdd`** — write the failing test FIRST, run it, watch it fail for
   the expected reason; then write the minimal implementation; watch it
   pass. No production code without a failing test. Follow
   `references/tdd-protocol.md` — the discipline is in its defenses against
   "just this once", not the one-line rule.
   **`tdd: direct`** — implement, then run the task's stated verification.
2. After verification passes: check the task off in `tasks.md` — the only
   file carrying completion state — then commit; one commit per task, message
   reflects design intent. Never batch tasks into one commit; never leave
   checked-off tasks uncommitted.

**The task list is live state — append before doing, check off at landing.**
The checkboxes are the change's ground truth; a fresh session resumes from
them alone, so any drift between the list and the repository is how a session
gets lost. Four rules, no exceptions:

- **No work outside a written task.** Discovered work — a missing edge case,
  a prerequisite refactor, a test the plan forgot — is APPENDED as an
  unchecked `- [ ] N.M` in `tasks.md` **and** a matching `## Task N.M` detail
  block in `plan.md`, in the same edit, **before** any of its code is written.
  Append-then-do, never do-then-maybe-note. A few lines inside the current
  task's stated scope belong to that task; anything more is a new task.
- **Check off at landing, in the task's own commit.** The `tasks.md` checkoff
  rides the commit that completes the task — never before the commit, never
  batched afterwards. `plan.md` has no checkbox to update.
- **Never renumber, reorder, or delete tasks.** A task that becomes
  unnecessary is checked with a one-line reason
  (`- [x] N.N SUPERSEDED: <why>`); appended tasks take the next number.
  Stable numbering is what makes "first unchecked task" a reliable resume
  point.
- **Fix the list before writing more code.** If at any moment the checkboxes
  do not describe reality (an unchecked task is actually done, work happened
  that no task names), stop and reconcile the list first — that state is a
  live defect, not cleanup for later.

Appended tasks that touch the design's interfaces, components, or data flow
are not "discovered work" — they are scope changes; route them through
section 5 instead of appending silently.

**Delegate review and parallelize independent tasks.** The reviewer role above
is the `onto-reviewer` subagent shipped with onto — hand it each task's diff (and
always the final diff), rather than reviewing inline. Its findings are input to
**evaluate, not execute**: apply `references/receiving-review.md` — verify each
finding against the code before acting, and push back with evidence on a wrong
one instead of implementing it. When `plan.md` marks tasks
whose file sets **do not overlap**, dispatch their reviews (and any needed
`onto-explorer` investigation) **concurrently** — one subagent invocation per
task — via the Task tool (OpenCode runs each as a child session; Claude Code runs
parallel Task agents when you send several calls in one turn), so the reviews
proceed in parallel while you implement the next task. Tasks that share files
stay serial (one commit each, in order).

A diff worth more than one opinion gets **several reviewers at once, one per
lens** (correctness, security, contract/scope, clarity) rather than one
generalist pass — they are read-only, so concurrency costs nothing but tokens.

This is the concrete wiring of the dispatcher's "Delegation, parallelization,
and dialogs" section. Under **`execution: direct`** the orchestrator (this
session) owns every edit and commit and the subagents only read and report;
under **`execution: subagent`** implementers edit and commit their own task's
files. The orchestrator owns every `onto` binary call in both modes.
Use the question dialog for the plan-ready and scope-change decisions when it is
available.

### 4. Failure gate (systematic debugging)

On ANY build/test/unexpected failure: stop and follow
`references/debugging-protocol.md`. No source fix may be proposed or applied
before the **root cause** is identified (reproduce → read the whole error →
check recent changes → trace the data flow). If the root cause is a source bug,
add a minimal failing test that reproduces it, then fix, then watch it pass.
Symptom-patching is prohibited; after 3 failed hypotheses, escalate a
fix-vs-rethink decision to the user rather than guessing again.

### 5. Mid-build scope changes

- Small (missing edge case, scenario): edit the delta spec + design.md
  inline, append a task, note it in the commit message.
- Medium (interface/component/data-flow changes): pause, get user
  confirmation, then — **in this order, so the derivation table's
  `Under revision` row wins at every intermediate state** — (1) flip
  `design.md`'s status line to `Status: Under revision`, (2) if a
  `verification.md` exists, flip its `Result:` line to
  `Result: superseded (revision <date>)` and run `onto set verify-result
  <name> pending` (the cache must not keep claiming a pass the file has
  withdrawn), (3) the `Status: Under revision` marker now drives the
  dispatcher's derivation to `design` (files win downward) — no phase field
  is written; the next dispatch routes to design. A stale pass can then
  never teleport the revised change past build/verify. The derivation
  routes to design until the approach gate re-confirms (new
  `Status: Confirmed` + date), after which build resumes.
- Large (new capability, or new tasks exceed half the original task count):
  pause; the user chooses between splitting into a new change or expanding
  this one. Always fresh input.

## Exit checklist

- [ ] Every `tasks.md` item checked (or explicitly marked deferred-to-close
      with the reason **and** a one-line statement of why it is non-runtime
      work — the close lint blocks runtime-behavior deferrals)
- [ ] One commit per task; working tree clean — including the workspace
      docs (tasks/plan/notes updates ride their task commits; anything
      still uncommitted in `docs/changes/<name>/` commits now)
- [ ] Project build + test suite run fresh and pass (state the commands and
      results — do not rely on memory)
- [ ] Decisions recorded via `onto set isolation|build-mode|tdd-mode <name> …`
- [ ] Phase advanced build → verify via `onto advance <name>`
- [ ] Announce the transition and load `onto-verify`
