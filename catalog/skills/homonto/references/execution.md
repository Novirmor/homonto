# Task execution and evidence

Use this policy in both workflows. The dispatcher chooses the path; the phase
owns its actions; this reference owns dispatch fallback, task granularity, and
evidence reuse. Follow [autonomy](autonomy.md) and the
[workspace policy](workspace-policy.md) throughout.

## Dispatch capability

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
