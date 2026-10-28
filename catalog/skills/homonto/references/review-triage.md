# Review triage and bounded rechecks

The coordinator owns finding scope and severity. Before a finding blocks the
requested endpoint, identify its supported scenario, violated requirement or
existing safety invariant, concrete evidence and impact. Ground implicit
invariants in actual behavior; a reviewer's label does not create a requirement.

Supply reviewers and skeptics the supported environments/scenarios, explicit
non-goals, accepted decisions and prior finding IDs/dispositions alongside the
candidate and full evidence pack. Missing evidence is a request to investigate
or run a discriminating probe, not proof of a defect.

## Proportionality before repair

Judge each proposed repair against the **current user request**, not the whole
PR's ambitions or everything a skeptic can improve. Before treating a finding as
repair work, establish whether it was introduced by the assigned change or is
necessary to satisfy that request. A pre-existing defect or weak test is not
automatically part of the assignment merely because it is in the same PR.
When origin, impact or the repair boundary is unclear, use the selected engine's
explorer under [focused exploration](execution.md#focused-exploration) to establish
those facts before accepting the finding or asking the user a technical question.

For a **conflict-resolution-only** request, preserve the intended behavior of both
sides and verify the resolution. Repairing a regression caused by the resolution
is in scope; tightening otherwise unchanged PR tests, unrelated bug fixes,
refactors and general hardening are not. Running a relevant check or bounded
diagnostic probe does not authorize changing everything it uncovers.

Check **cumulative repair scope**, not just each small finding: extra rounds must
not turn a narrow task into a broad cleanup. If a proposed repair materially
expands behavior, acceptance criteria, affected components, effort or risk beyond
the request, ask through OpenCode's built-in `question` tool **before appending or
dispatching repair tasks** or making those edits. Explain the finding, why the
larger repair is needed, its expected scope/cost, and the bounded alternative
(including leaving it as a follow-up). Wait for explicit approval; no answer is
not consent. Routine necessary, proportionate in-scope repairs still proceed.

Record optional unrelated improvements as follow-ups without implementing them;
do not interrupt for every suggestion. A blocking finding does not authorize its
repair outside the agreed scope: report the blocker and ask for the needed scope
decision rather than silently expanding the task or claiming a clean result.

## Finding dispositions

- Confirmed in-scope defect: record a repair task and continue through its owning
  engine, then refresh affected evidence. Real data loss, credential exposure and
  failed core acceptance remain blocking even for a local-only tool.
- Unsupported scenario, out-of-scope demand or duplicate: record the contract,
  evidence or prior finding ID that justifies dismissal and continue.
- Evidence request: supply readable artifacts or the exact probe's command,
  inputs, exit status and output; then triage the result.
- Speculation that remains ungrounded after investigation: dismiss with the
  reason. Do not silently accept a known defect as if it were speculation.

Existing engine rules govern acceptance of deviations and non-waivable defects.
For repairs that pass the proportionality and scope check above, ask only when
accepting a lower-severity deviation is justified by a user-owned constraint;
otherwise repair. An authorized in-scope finding or unfinished check is remaining
work, not an endpoint or a reason to ask whether to continue.

After repair, provide the new candidate, complete updated pack, stable finding
IDs, repair evidence and prior dispositions. Required final passes still cover
the final candidate, focusing fresh investigation on repairs, affected claims
and adjacent regression risks. Retain unchanged evidence only when provenance
and inputs permit it; rerun affected configured integration checks. Preserve
native report/receipt requirements.

Do not reopen a dismissed finding without new evidence or changed scope. Expand
a recheck only when repair impact justifies it, not into an unrestricted new
audit. This bounds scope, not the number of real defects that may be reported.
