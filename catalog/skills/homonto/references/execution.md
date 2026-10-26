# Task execution and evidence

Use this policy for the selected engine. The dispatcher chooses the path; the phase
owns its actions; this reference owns dispatch fallback, task granularity, and
evidence reuse. Follow [autonomy](autonomy.md) and the
[workspace policy](workspace-policy.md) throughout.

## Dispatch capability

### Parallel repository assignments

Installations may contain onto, to and no together. A task selects exactly one
engine; preserve it on resume and ignore the other engines for that task. No
has no binary, plan or lifecycle state. Onto/to retain their native lifecycle
gates and registered execution bindings. A preset escalation inside onto does
not switch engines. Do not convert an active task into another engine.

The parent model chooses dependencies and concurrency, across repositories
first: exactly one repository per implementer assignment, one writer per
checkout. Supply selected engine, writable alias, absolute checkout, base,
contract and verification, plus all declared repository paths as readable
context. Assignment scope governs writes; it is not a runtime sandbox. There
is no scheduler or cross-session checkout reservation. Serialize same-repo work
and parent-owned workflow mutations. Continue independent assignments around
blockers while pausing their dependents.

Ask the user once per task for existing checkouts versus worktrees, with per-repo
exceptions, unless the request or recorded decision already selects them; reuse
that choice on resume and at every phase. Clean checkouts do not waive the choice.
Native onto `branch` means execution in the existing checkout on a safe source
branch; `worktree` uses registered allocations. Preserve per-repo exceptions in
workspace-task metadata rather than overwriting them with a phase default.
In schema 2, record it with
`homonto workspace task create NAME --engine ENGINE --isolation MODE --repo ALIAS`
(repeat `--repo`, add `--change CHANGE` for onto/to). Use the actual `--config`.
Inspect/resume with `homonto workspace task inspect NAME`, not a new allocation.
Dirty unchanged verification inputs require a user disposition; an approved
committed snapshot uses `--isolation-override ALIAS=worktree`. Never transport
dirty input silently. Shared metadata records checkout facts and results, not
plans or lifecycle phases. Follow native allocation ordering under workspace policy.

The parent owns all source commits, branch integration and publication. Workers
return edits and verification evidence. Review completed per-repo candidates in
parallel while those checkouts have no writers, then review the combined
interfaces and integration results. Commit candidates before integration checks.
Pause every participating writer; unrelated work may continue. Run
`homonto workspace task check NAME --writers-paused`. Named configured checks
match when ANY listed repo changes; unchanged participants stay pinned to their
clean committed revisions. Commands receive `HOMONTO_INTEGRATION_CONTEXT`, a
JSON file with actual checkout paths and revisions. Changes invalidate affected
results. Model-selected fallback applies only when no configured check matches,
never to a configured failure. Ask before delivering a partial subset.

After authorized integration, task cleanup removes only clean integrated owned
allocations; onto/to native bindings still require their terminal lifecycle and
`homonto worktree remove`. No worker performs allocation or cleanup.

Check actual host dispatch and the required worker before assigning work. Apply
the following table to implementation, review, and final skepticism in both
workflows, including re-verification after integration:

| Situation | Action |
|---|---|
| Dispatch and worker available | Use the phase's assigned worker; validate its result against the repository |
| Dispatch or worker unavailable | Coordinator implements or reviews directly; record which independent pass was unavailable and perform its checks without claiming an independent verdict |
| Repository or user explicitly requires independent review | Missing dispatch/reviewer is a blocker; ask only for a real authorization decision, never silently waive it |
| Tool permission denied | Honor the denial; no direct execution or alternate tool as a workaround |
| Worker failed or returned blocked | Resolve evidence requests and retry with the complete pack; an unsuccessful attempt is not absence and is not a completed pass |

Use one worker per focused question, with bounded concurrency appropriate to
scope and cost. Small source tasks may run directly. With dispatch available,
to uses a task reviewer and at least one completed final skeptic; full onto uses
its two final skeptic lenses, while light onto permits an optional skeptic.
Technical inability must be recorded as a gap, not a passing worker verdict.
An exhausted recovery is a factual blocker under autonomy.

Every worker receives Owner, Repo, absolute Cwd, relevant contract, exact candidate
or diff, and readable evidence. The coordinator owns records and workflow calls.
Source-only workers never edit records. Existing permissions and write scopes
remain binding in direct mode.

## Reviewable tasks and commits

Tasks describe independently verifiable outcomes. Split distinct outcomes or
uncertain ownership, not a fixed number of lines. Keep implementation and focused
tests together. Generated files can make a cohesive change large without creating
another task. Default to one focused commit per task; a cohesive commit may land
several tightly coupled small tasks if it names them and records each verification.
Never check off a task before its assigned work and evidence have landed. Keep
source and records ownership separate under the workspace policy.

Append discovered work before doing it. Keep stable task IDs and completed history;
do not renumber, delete, or rewrite completed tasks to hide prior decisions.
When a new prerequisite blocks the next task, record the dependency and next
executable task in notes before dispatch, without moving checkbox entries.
On resume, use that explicit dependency order; otherwise start at the first
unchecked task. Restore normal order once the prerequisite is complete. A
superseded task keeps its ID and reason. Full onto keeps task/plan pairs in sync;
presets keep inline contracts and do not acquire a plan merely for a repair.

## Candidate-bound verification

Fresh evidence identifies the source candidate/tree, command, cwd, relevant
inputs/environment, exit status and actual output. Evidence from this invocation
may satisfy multiple checkpoints when all those inputs are unchanged and its
coverage matches the claim. Do not rerun a check merely because a phase changed.
After compaction, validate recorded provenance before reuse; uncertain provenance
requires a rerun. Changed source, test inputs, dependencies, environment, time-sensitive
external state, or merge/conflict resolution invalidates the affected evidence.

Each final phase checks coverage, runs missing or invalidated checks, and records
honest gaps. A final skeptic receives the complete final-candidate evidence pack;
an earlier task review does not replace it. Changed candidates require fresh final
review under the dispatch policy above. Onto's required scenario evidence, report
hashes and receipts still apply: regenerate affected reports/receipts, never claim
that an old artifact hash covers an edited report.

## Implementer return

The coordinator supplies this contract with each task, pasted or as a
worker-readable absolute path; do not assume inherited context. Return a concise
message for the coordinator, not a new report file or a claim that the workflow
is done. Include:

- **Outcome:** complete, partial, or blocked for this assignment, not the whole workflow.
  Name the task and preserve any handed task ID and trace marker.
- **Changes:** files changed, what changed and why. Identify the uncommitted
  candidate with its base and exact diff, including new files. The parent commits.
- **Verification:** for each check, identify the tested candidate, absolute cwd,
  literal command, relevant inputs/environment, exit status, and literal verification
  output or a readable output path. State which expected passing signal was
  observed and what it proves. Keep failure and skip information intact; an exit
  status of zero when no tests ran is not evidence that the behavior passed.
- **Gaps / discovered work:** checks that did not run, unavailable evidence,
  remaining failures, and needed work outside scope — reported, never done.
- **Questions:** only unresolved goal, scope, ownership or authorization decisions.
  Technical blockers belong with their evidence under gaps; do not invent a user
  decision. Return a unified diff if requested.

Illustrative returns (replace the sample evidence with the task's actual results):

> **Outcome:** complete — task 1.2, reject an empty name.
> **Changes:** parser.go and parser_test.go; uncommitted candidate at base `<SHA>`
> plus the supplied diff (including the new test file).
> **Verification:** that candidate, cwd `/work/app`, `go test ./...`, Go `<version>`,
> dependencies from go.sum, exit 0; output `ok example/app 0.012s`.
> The empty-name regression and package suite ran and passed.
> **Gaps / discovered work:** integration tests did not run; not required by this task.
> **Questions:** none.

> **Outcome:** partial — task 1.2; implementation present, verification incomplete.
> **Changes:** same uncommitted candidate and diff supplied above.
> **Verification:** cwd `/work/app`, `go test ./... -run '^TestEmptyNmae$'`,
> same candidate and inputs, exit 0; output `ok example/app 0.003s [no tests to run]`.
> No regression was exercised; this is not a passing verification of the fix.
> **Gaps / discovered work:** run the correctly named regression and required suite
> before completion. If still able to execute, correct the selector and run them
> before returning; an avoidable command typo is not a blocker.
> **Questions:** none.

The coordinator validates the diff and evidence against the actual candidate
under the verification policy above. A formatted worker report alone never
establishes completion.
