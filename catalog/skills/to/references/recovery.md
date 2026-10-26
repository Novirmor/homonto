# Recover to delivery

Use this before creating new work and when resuming a named task or PR.
`to status --json` lists active tasks only; neither it nor `--all` lists archives.
Inspect matching directories under `<workflow-root>/tasks/archive/` directly,
using the generated workspace roots. Read their state and full `plan.md`.

Match the requested task identity, selected source aliases, recorded base and
candidate/tree OIDs, and intended integration targets. For PR work also match
canonical host/repository ID/number and head/base identity. Never choose an
archive by newest date, branch name alone or whether local HEAD is ahead of
upstream. Multiple plausible generations or missing candidate evidence require
investigation and a concrete identity decision if still ambiguous.

An abandoned archive is unsuccessful history. Never use it as verified
publication input or resume it without explicit new intent. For `done`, locate
the recorded verified source candidate and compare it with the actual receiver
and remote. Keep these states separate:

| Observation | Route |
|---|---|
| Terminal state still in active tasks directory | Recover pending managed history with `homonto workspace recover`, inspect `to doctor`, retry the terminal move using existing verification or explicit abandonment intent |
| Archived candidate not integrated | Load `to-done` at integration, validating the recorded candidate and receiving checkout |
| Integrated but not pushed / PR missing | Load `to-done` at the first missing authorized delivery action; reverify changed receiving trees |
| Delivered but requested remote readiness incomplete | Observe exact-head required checks and PR state; pending CI is not success |
| Requested endpoint already satisfied | Report the observed evidence; only genuinely new authorized work needs another task |

Archival alone is not completion. Confirm the archive exists and the active path
is gone, then reconcile delivery before routing new work. Do not rerun `to done`,
assert fresh verification merely to finish a move, or edit an immutable archive.
Preserve source candidates and record post-archive observations in the handoff.
An in-scope source repair after archive uses a linked follow-up under the same
selected engine; it cannot replace the old archive's verified candidate.

When the requested endpoint is a review-ready PR, follow the shared publication
contract and observe its canonical identity, OPEN/non-draft status, delivered
verified head and required checks passing on that head. Establish which checks
are required; missing status data is not proof none are required. Watch pending
checks with a bounded timeout, investigate failures and repair in scope. Preserve
an explicitly requested draft. Report a concrete external wait or blocker when
readiness cannot be established; never report pending CI as completion.
