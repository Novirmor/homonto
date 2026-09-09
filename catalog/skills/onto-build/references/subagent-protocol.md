# Subagent build protocol (`build_mode: subagent`)

Coordinator/worker execution for the build phase. **The main session never
implements source tasks in subagent mode**; it plans, dispatches, verifies, and
keeps state true. Substantial `Owner: coordinator` workflow-record tasks are an
explicit separate lane: the coordinator edits and verifies named specs, ADRs,
guides, and plans serially, with read-only review. Implementers remain prohibited
from editing records. Split mixed-root tasks before dispatch.

The framework ships the two agents this protocol uses, each with an enforced
capability profile (homonto renders it per tool):

- **`onto-implementer`** — the worker. Edits, runs build/test, **spawns
  nothing**. Hand it one task's spec; it returns a diff.
- **`onto-reviewer`** — the reviewer. Read-only with no shell; the coordinator
  supplies the exact diff and relevant files.
  (Each agent's model comes from the installer's `[subagents.<name>.<tool>]`
  block, not from this protocol.)

**A subagent never prompts the user.** If the implementer hits an ambiguous spec,
it returns a `Questions:` section. The coordinator resolves factual or technical
uncertainty from repository evidence and asks the user only when product intent
is missing, then re-dispatches.

## When to choose subagent over direct

- Many independent tasks (≳4) or tasks touching disjoint files
- Main-session context is precious (long-running change, big design)
- Tasks benefit from fresh eyes (no accumulated assumptions)

`direct` remains right for small serial changes where dispatch overhead
exceeds the work.

## Per-task dispatch

**Default path: serial. One task at a time, strictly in
plan order.** Two implementers on one branch share the working tree and Git
index, so their edits and commits can race. The coordinator alone writes
workflow and task records. Fan out implementers only under the legacy combined
exception below; if the mode or ownership is uncertain, stay serial.

Follow the shared [workspace and dirty-work policy](../../homonto/references/workspace-policy.md).
The coordinator supplies configRoot, records root, selected alias, validated
execution binding (or the assigned legacy task worktree), and the existing dirt
decision. Workers never allocate or remove workflow execution/receiver worktrees,
transport dirty input, or checkpoint records themselves. Task-authorized Git/gh
setup and reads are allowed within their assigned write scope, including isolated
Git fixtures under the shared policy, not authoritative GitHub intake or
publication. Trusted shell scripts, Python/Node, chains, and pipes do not change
these ownership or delegation boundaries.

Dispatch ONE `onto-implementer` per task (a fresh context each time), whose
prompt contains:

1. The task text verbatim (Owner, Repo, absolute Cwd, files, do, verify) from `plan.md`
2. The relevant `design.md` section(s) — pasted, not summarized
3. The isolation target: the exact branch (and worktree path, if any) to
   work in — a fresh-context agent must never guess where to commit
4. Conventions: one implementation commit containing only this task's assigned
   source and test files, message style from recent
   `git log`, match surrounding code idiom
5. The TDD rule in force (`tdd: tdd` → failing test first, watch it fail)
6. The debugging rule: on any failure, root cause before any fix —
   reproduce, read the whole error, trace; no symptom-patching
7. The bookkeeping boundary: do not edit `tasks.md`, `plan.md`, or workflow/state
   records. Preserve the handed dotted task ID and its `[trace #N]` marker in
   the return; the coordinator records completion after verifying the task
8. Return contract: commit sha + diff summary + literal verification
   output + any **discovered work** (needed work outside this task's
   stated scope) — reported, never done. The coordinator appends each
   reported item as an unchecked `- [ ] N.M <task> [trace #K]` in `tasks.md` plus its matching
   `## Task N.M` block in `plan.md` (or routes it through the scope-change
   gate) BEFORE the next dispatch, so the task list never trails what the
   repository already knows.

## Schema 2 registered allocation

The allocator supports one binding per workflow/change/repo. Task-level bindings
are not supported. Run same-repo tasks serially, even for disjoint files.
No unregistered raw/native workflow execution worktrees are permitted in schema 2,
in either existing or managed history mode. Read-only workers may fan out. Separate selected
repos need disjoint write scopes and validated bindings, with coordinator
bookkeeping serialized after verifying each return.

## Legacy schema 0/1 combined parallel

Only for an existing combined source/records workflow using config schema 0 or 1
(including an omitted schema version), `build_mode: subagent`,
`isolation: worktree`, and tasks touching disjoint files. This is the retained
legacy capability, not a fallback from the schema 2 allocator. A legacy state
file under schema 2 does not enable it. Never downgrade configuration, switch
tools, or use this protocol to work around a denial or a failed registered binding.

All five conditions must hold, or stay serial:

- [ ] One **git worktree per implementer**, with a distinct branch and explicit
      committed base; never two writers in one working tree or index.
- [ ] Implementers **do not touch `tasks.md`/`plan.md`** or workflow/state records;
      they commit only their assigned source and test files.
- [ ] The coordinator merges the worktree branches into the change branch
      **in plan order**, validating each returned source commit before joining.
- [ ] The coordinator performs **every** bookkeeping checkoff and commit itself,
      serially, after the merges, before the next batch or serial dispatch.
- [ ] Reviewer dispatches, including the mandatory final-task review, run
      **only after the last join**, against the integrated candidate.

The coordinator alone owns workflow state in both modes. Its combined change
checkout remains configRoot and the authoritative records location; task
worktrees contain read-only copies of those records, never competing state.
Follow `worktree-protocol.md` for creation, baseline checks, joins, and authorized
cleanup. Dirty input still requires the user's transport decision; never copy
`.env` or untracked files automatically.

## Coordinator duties after each return

- **Verify against the repository, not the report**: the returned commit
  sha exists (`git log`), its diff contains only assigned source and test files
  with no task or workflow-state mutations, the working tree is clean, and the
  stated verification output is plausible
  (spot-run it when cheap).
- **Record completion only after verification**: the coordinator records the
  implementation commit SHA and verification commands/results, checks the task
  off in `tasks.md`, and commits the bookkeeping separately before the next
  dispatch in existing mode. In managed mode use
  `homonto workspace checkpoint --path changes/<name> --message "Record task completion"`
  instead of a manual records commit. Preserve the dotted task ID and `[trace #N]` marker; `plan.md` has
  no checkbox. The coordinator owns every workflow-state write through the
  binary and any required evidence recording. On resume, verify an existing
  implementation commit and finish its missing bookkeeping instead of
  reimplementing it. For a legacy parallel batch, perform these checkoffs and
  bookkeeping commits serially after all ordered joins, before the next dispatch.
  Source commits and managed history remain separate.
- A failed or half-done task is re-dispatched with the failure context after
  root-cause analysis — never silently absorbed. Before
  re-dispatch, preserve and attribute partial work under the existing dirt
  decision. Cleanup requires approval of the exact named action and paths;
  apparent disposability is not permission to reset. Hand approved input to the
  replacement agent explicitly as part of the failure context. Never stash or discard
  unattributed work to manufacture a clean tree.

## Reviewer agents

After any task marked `(risk: high)` — and always after the final task —
dispatch `onto-reviewer` with the diff range and the design section (it is
already prompted to **find faults** — correctness, spec conformance, missed edge
cases — never to approve). CRITICAL findings are fixed via a re-dispatched
`onto-implementer` before the next source task (records fixes stay coordinator-owned);
accepted non-critical findings are recorded in the plan or commit body.
Apply `receiving-code-review` discipline to the findings: verify each against the
code before acting, and push back with evidence on a wrong one rather than
implementing it blindly.

For a legacy parallel batch, wait until the last join before these reviews;
review the integrated candidate, not a task tree still being written.

## Failure of the protocol itself

No real dispatch capability available → record the fact, fall back to
`build_mode: direct` in `onto-state.yaml` (via `onto set build-mode <name>
direct`), announce, continue.
