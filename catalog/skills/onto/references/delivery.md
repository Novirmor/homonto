# Onto delivery loop

The coordinator owns this loop across full, fix and tweak, including direct phase
entry and recovery. Phase skills supply the procedures; a worker's return hands
control back to the coordinator, not to the user.

## Capture the endpoint

At setup, or on resume if missing, record in `notes.md` the requested endpoint,
supported scenarios, explicit non-goals, participating repos, delivery targets
and existing publication authority. Reuse confirmed intent. A phase command is
an entry point unless the user explicitly makes it an earlier endpoint.

For a **review-ready PR**, completion means verified commits delivered to the
intended open, non-draft PR, required checks passing on its current head, and no
confirmed in-scope blocking findings. Complete any requested summary and feedback
resolution as well. Human approval and merge come afterward. Local integration
or an explicit verify-only endpoint does not acquire a PR requirement.

## Check delivery prerequisites early

Before freezing bases at creation, validate source aliases, canonical repository
identities, intended receiving branches and existing-PR head identity. A diff
anchor and a delivery branch are different things. Follow
[publication](../../homonto/references/publication.md) for exact target mapping
and authority; do not retarget immutable bases to repair a mismatch.

When deltas are drafted, and again before final verification, read their target
living specs under the configured records root. Check exact requirement names
for MODIFIED, REMOVED and RENAMED, and collisions for ADDED; account for rename
order. Run the phase-applicable read-only parts of
[close lint](../../onto-close/references/lint-checklist.md). Also validate source
mapping, guide obligations and the intended delivery route. This is inspection,
not spec merging: `onto merge-deltas` has no dry-run flag and runs only at close
after the close-plan review.

If a living requirement is missing, investigate the configured root, history,
capability name and operation. Correct an evidenced mapping/delta error while
active; ADDED is appropriate only for a genuinely new requirement. Never invent
a baseline or relabel MODIFIED merely to clear a gate. Ask only if the intended
contract or recovery needs a user decision. Refresh affected verification if the
correction changes the contract or candidate. Recheck prerequisites at close;
early inspection does not replace close gates.

## Continue from every result

Maintain finding IDs, dispositions, evidence and the next action in the existing
verification report/notes, not another state file. Apply
[skeptic triage](../../onto-verify/references/adversarial.md#triage-coordinator-into-the-report).

| Observed result | Coordinator's next action |
|---|---|
| Tests fail or a confirmed in-scope defect remains | Add a precise repair task, repair through the owning full/fix/tweak route, then reverify |
| Verification is unchecked, stale or incomplete | Run the missing checks or obtain the missing evidence now |
| Skeptic requests evidence | Supply readable files or run the exact discriminating probe and return its output |
| Finding is unsupported, duplicate or outside agreed scope | Record evidence-backed disposition; continue the remaining work |
| Verification passes | Close, integrate and publish within recorded authority |
| Close or publication fails | Investigate the actual refusal and attempt permitted recovery; resume at the failed step |
| Native state derives done | Check the requested endpoint against observed delivery facts |
| Required remote checks are pending | Watch with a bounded timeout; inspect failure or stalled execution and recover when authorized |

A skeptic report, failing test, unchecked final verification or recorded repair
task is remaining work, not a stopping condition. Execute the next action in the
same invocation. After repairs, refresh affected evidence and use bounded
skeptic rechecks; do not restart an unrestricted audit of unchanged scope.

Stop only at the requested endpoint, an explicit pause, a concrete missing
decision or permission, or an external blocker after permitted recovery. Name
the exact unavailable action, attempted recovery, evidence, preserved state and
resumption condition. Pending CI that exceeds a bounded watch is an external
wait, not success. A repeat-failure count triggers investigation/replanning, not
automatic surrender. Checkpointing and a progress summary do not replace action.

## Confirm review readiness after publication

The coordinator reads authoritative remote state under the shared publication
contract. Verify canonical PR identity, OPEN state, non-draft status, intended
base/head refs and that the observed head is the delivered verified candidate
(or an explicitly permitted, proven descendant). Inspect required checks and
their results for that exact head; a prior green run or local suite is not remote
CI proof. Establish which checks are required; an empty or unavailable status
list is not proof that none are required. Record an evidenced absence of required
checks when applicable. If the head changes, reconcile before claiming readiness.

Watch pending checks with a bounded timeout. Diagnose failures and repair
in-scope causes; do not bypass checks or rerun blindly. A draft requested by the
user remains draft; changing that endpoint requires their decision. Otherwise
mark ready only within publication authority and tool permissions. Preserve
linked-repo delivery obligations and required integration checks; do not call a
partial delivery complete. Resolve only actually addressed feedback, using the
intake skill's authorized summary/thread procedure.

`derived_phase: done` and a PR integration receipt prove native integration
bookkeeping, not remote CI or review readiness. Resume remote readiness checks
even when native state is done. A source repair after archive needs a linked
follow-up change under the existing engine and normal selection/intent rules;
the requested endpoint still owns the remaining work. Never reopen or rewrite
an immutable archive to attach a new candidate. If new selection or authority
is genuinely missing, ask that specific question rather than whether to continue.

Keep active progress in notes; after archive retain readiness observations in
session publication evidence and the final handoff, or a linked follow-up's notes.
The sanctioned neutral `ship.md` addition stays immutable once committed; never
mutate archived verification or notes to record new remote results. At the
endpoint report PR URLs, delivered head OIDs, observed check results and any
accepted deviations. If readiness could not be established, report the concrete
blocker without claiming the PR is ready.
