# Subagent build protocol (`build_mode: subagent`)

Coordinator/worker execution for the build phase. **The main session NEVER
implements** — it plans, dispatches, verifies, and keeps state true.

The framework ships the two agents this protocol uses, each with an enforced
capability profile (homonto renders it per tool):

- **`onto-implementer`** — the worker. Edits, runs build/test, **spawns
  nothing**. Hand it one task's spec; it returns a diff.
- **`onto-reviewer`** — the reviewer. Read-only, keeps bash for inspection.
  (Each agent's model comes from the installer's `[subagents.<name>.<tool>]`
  block, not from this protocol.)

**A subagent never prompts the user.** If the implementer hits an ambiguous spec
it **returns** the question (a `Questions:` section), and the coordinator asks the
user (via a dialog) and re-dispatches — a Claude Task subagent cannot prompt
mid-run, so this protocol is identical in both tools.

## When to choose subagent over direct

- Many independent tasks (≳4) or tasks touching disjoint files
- Main-session context is precious (long-running change, big design)
- Tasks benefit from fresh eyes (no accumulated assumptions)

`direct` remains right for small serial changes where dispatch overhead
exceeds the work.

## Per-task dispatch

**Default and only safe path: serial. One task at a time, strictly in
plan order.** Every task writes the shared bookkeeping files (tasks.md,
plan.md), so two implementers on one branch race and corrupt them. Do not
fan out. If you are unsure, you are serial.

Parallel dispatch is a narrow exception with its own protocol below — it
is not "run the tasks at once," and skipping any of its conditions
reintroduces the race it exists to avoid. Ignore it unless you have
deliberately chosen it and can meet every condition.

Dispatch ONE `onto-implementer` per task (a fresh context each time), whose
prompt contains:

1. The task text verbatim (files, do, verify) from `plan.md`
2. The relevant `design.md` section(s) — pasted, not summarized
3. The isolation target: the exact branch (and worktree path, if any) to
   work in — a fresh-context agent must never guess where to commit
4. Conventions: one commit for this task, message style from recent
   `git log`, match surrounding code idiom
5. The TDD rule in force (`tdd: tdd` → failing test first, watch it fail)
6. The debugging rule: on any failure, root cause before any fix —
   reproduce, read the whole error, trace; no symptom-patching
7. The bookkeeping obligation: after verification passes, check the task
   off in `tasks.md` — the only file holding completion state; `plan.md`
   has no checkbox — then commit; files, not chat
8. Return contract: commit sha + diff summary + literal verification
   output + any **discovered work** (needed work outside this task's
   stated scope) — reported, never done. The coordinator appends each
   reported item as an unchecked `- [ ] N.M` in `tasks.md` plus its matching
   `## Task N.M` block in `plan.md` (or routes it through the scope-change
   gate) BEFORE the next dispatch, so the task list never trails what the
   repository already knows.

## Parallel dispatch (exception — meet every condition or stay serial)

Only for tasks touching **disjoint files**. All of the following hold, or
you do not parallelize:

- [ ] One **git worktree per implementer** — never two implementers on one
      working tree or branch.
- [ ] Implementers **do not touch `tasks.md`/`plan.md`** (drop item 7 from
      their prompt) and commit only their own task's files, in their
      worktree.
- [ ] On join, the coordinator merges the worktree branches into the change
      branch **in plan order**.
- [ ] The coordinator performs **every** bookkeeping checkoff and commit
      itself, serially, after the merges.
- [ ] Reviewer dispatches (including the mandatory final-task review) run
      **only after the last join**.

One commit per task is preserved either way. If you cannot check every box,
run serial — the shared-file race corrupts `tasks.md`/`plan.md` silently
and is not worth the speed.

## Coordinator duties after each return

- **Verify against the repository, not the report**: the returned commit
  sha exists (`git log`), the `tasks.md` checkoff landed, the working
  tree is clean, and the stated verification output is plausible
  (spot-run it when cheap).
- A failed or half-done task is re-dispatched with the failure context, or
  taken through the failure gate — never silently absorbed. Before
  re-dispatch, restore a clean tree: stash/reset the partial work, or hand
  it to the replacement agent explicitly as part of the failure context —
  a fresh agent must never inherit dirty state unknowingly.

## Reviewer agents

After any task marked `(risk: high)` — and always after the final task —
dispatch `onto-reviewer` with the diff range and the design section (it is
already prompted to **find faults** — correctness, spec conformance, missed edge
cases — never to approve). CRITICAL findings are fixed via a re-dispatched
`onto-implementer` before the next task (the coordinator still never implements);
accepted non-critical findings are recorded in the plan or commit body.
Apply `receiving-code-review` discipline to the findings: verify each against the
code before acting, and push back with evidence on a wrong one rather than
implementing it blindly.

## Failure of the protocol itself

No real dispatch capability available → record the fact, fall back to
`build_mode: direct` in `onto-state.yaml` (via `onto set build-mode <name>
direct`), announce, continue.
