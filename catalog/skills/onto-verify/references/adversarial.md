# Adversarial verification protocol

Self-verification shares every blind spot with the implementation that
produced it. Fresh-context skeptics exist to catch what in-session review
structurally cannot (v1 precedent: two dry-run agents found 11 real
defects self-review missed).

## When

- `verify.scale: full` → REQUIRED: **at least** two skeptics, dispatched in
  parallel, after the self-evidence table is drafted. Two is the floor, not
  the ceiling — see "More than two" below.
- `verify.scale: light` (a light change) → no skeptic. Record "light: no
  skeptic pass" in the report's Adversarial section. Work that needs a skeptic
  escalates to the full workflow; it is never added inside a light change.
- Unavailable dispatch/worker → apply the shared
  [capability policy](../../homonto/references/execution.md#dispatch-capability).
  Perform claim/gap checks directly and record the missing independent pass in
  Adversarial. Explicit independent-review requirements still block; denial and
  blocked/failed workers do not qualify as absence. Never claim a completed
  independent pass for direct review.

## The two mandatory skeptics

Both are the **`onto-skeptic`** subagent that ships with onto — the same
agent dispatched **twice, concurrently**, with a different **lens** named
in each dispatch (`conformance` and `robustness`). It denies edits and shell
commands so parallel skeptics cannot mutate the candidate. The coordinator
runs any exact probes they request and returns the literal output for a fresh
pass.

Both get a complete worker-readable evidence pack: Repo and absolute Cwd for
every source, frozen base and candidate OIDs, exact diff/supporting files,
delta specs and design (or preset proposal scenarios), task/notes context,
literal commands, exit statuses and full output, evidence table, regression
results, the current user request and assignment boundary (even when narrower than
the original PR), supported environments/scenarios, explicit non-goals, accepted decisions,
and known gaps/findings with their dispositions. Run regression before the final skeptic
dispatch so that it can attack the complete claim. Confirm pack consumption;
coordinator tool output is not automatically shared. A summary or one-line pack
is insufficient: supply readable artifacts and full output paths, checking access.
Re-dispatch after candidate changes or missing probes with the new complete pack
and a bounded recheck assignment (below). Both are prompted to **REFUTE, never approve** — an
approving skeptic has failed its job; "I could not refute X because
<evidence>" is the only acceptable positive form.

1. **Conformance skeptic** — attack the claims: for each scenario verdict,
   try to demonstrate the evidence doesn't hold (challenge command provenance,
   propose a different probe of the same behavior, check the implementation actually does
   what the scenario says rather than something adjacent).
2. **Robustness skeptic** — attack the gaps: edge cases the scenarios
   don't cover, drift/recovery paths, failure modes, order-dependence,
   within the supported environments and agreed contract. This includes real
   data-loss and credential-exposure paths in local development; it does not
   import unrelated production deployment requirements.

## More than two

Skeptics deny edits and shell commands. Add bounded, distinct lenses when their
expected coverage justifies the latency, context and token cost, in the same parallel
batch and named in the report's Adversarial section like the mandatory two:

- **abuse** — drive the shipped behavior as a hostile user would: untrusted
  input, secrets, path traversal, privilege, deletion and overwrite paths. Add
  for anything touching the projection engine, remote sources, or file removal.
- **data/migration** — existing state written by an older version, partial
  writes, crash points, rollback. Add for any schema or state-format change.
- **compatibility** — take a user's existing file or command and break it: a
  public API, config key, or CLI flag that changed shape without a stated
  migration. Add for anything a user's file could reference.

Redundant lenses waste tokens and bury real findings in agreement; a lens
that cannot name what it would look for is not a lens. Two skeptics with
distinct angles beat four that overlap.

### Skeptic lenses are not reviewer lenses

`onto-reviewer` in build also works "by lens" (correctness, security,
contract/scope, clarity) and the two sets must not be confused, because they
ask different questions of different material:

| | `onto-reviewer`, in build | `onto-skeptic`, in verify |
|---|---|---|
| Material | the diff, as written | the built system, running |
| Question | is this code right? | is the evidence true? |
| Output | findings on the change | refutations of a claim |

A reviewer's `security` lens reads the new code for defects. A skeptic's
`abuse` lens tries to make the finished thing misbehave. Running one does not
discharge the other, and a skeptic dispatched with a reviewer's lens name will
re-read the diff instead of attacking the claim — which is why the names here
are deliberately different words.

## Triage (coordinator, into the report)

Apply shared [review triage and bounded rechecks](../../homonto/references/review-triage.md).
The table below adds onto's scenario verdicts and native failure/deviation gates.

The coordinator owns scope and severity, not the reviewer's label. Before a
finding blocks delivery, identify its supported scenario, violated requirement
or existing safety invariant, concrete evidence and impact on this endpoint.
Ground an implicit invariant in actual behavior; do not invent a requirement to
justify a label. Missing evidence is an evidence request, not a proven defect.

| Finding | Action |
|---|---|
| Refuted claim (evidence doesn't hold) | That scenario's verdict → fail; failure gate applies |
| Confirmed in-scope defect, CRITICAL (core acceptance, data loss, security) | Must fix — back through the failure gate |
| Confirmed in-scope defect, non-critical | Fix now; ask only when accepting it as a deviation is justified by a user-owned constraint |
| Evidence missing or ambiguous | Run the discriminating probe or supply the missing artifact; then triage |
| Unsupported scenario, out-of-scope demand or duplicate | Record the contract/evidence or prior finding ID that justifies dismissal; continue |
| Unverifiable speculation after investigation | Note and dismiss with the reason |

For example, startup importing an image it never pulled, stale credentials after
supported recovery, or recommending destructive purge for an unrelated failure
are actionable defects. Requiring a local-only tool to support arbitrary manual
Helm overrides excluded by the contract is scope expansion. Token shape alone
cannot prove whether a SHA-256 value came from credentials; trace actual data flow
before claiming exposure. A local-only label never excuses proven credential
exposure or data loss.

Record each finding ID, scenario/requirement, evidence, coordinator severity,
disposition and next action. Dismissal means the claim is unsupported or outside
the contract, not acceptance of a known in-scope defect. Known lower-severity
deviations follow onto-verify's user-decision rule; non-waivable defects stay so.
Turn confirmed repairs into tasks and execute them through the
[delivery loop](../../onto/references/delivery.md); do not end with a findings list.

## Bounded rechecks

After repair, refresh candidate OIDs, diff, affected scenario evidence, regression
and matching configured integration checks. Reissue the complete pack with finding
IDs, repairs and prior dispositions. Both mandatory lenses still cover the final
candidate in full mode; focus their fresh passes on the repairs, affected claims
and adjacent regression risks. Retain unchanged evidence only with valid
candidate/input provenance under the execution policy. Rebind report receipts as
onto-verify requires even when command output is reusable.

Do not reopen dismissed findings without new evidence or changed scope. A new
finding must meet the same supported-scenario and evidence bar; finite scope is
not a finding quota or permission to suppress a real defect. Expand a recheck
only when the repair's impact justifies it, not into an unrestricted new audit.

One verify round = self-evidence + skeptics together. `onto set verify-result
<name> fail` owns the single `observed.verify_rounds` increment. Findings that
force a fix start a new round.
