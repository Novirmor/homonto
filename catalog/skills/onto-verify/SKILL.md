---
name: onto-verify
description: onto phase 4 — verify. Use when an active change has phase verify (all tasks checked) — picks a verification level from change scale, checks implementation against design and every spec scenario with fresh evidence, and writes verification.md.
---

# onto-verify — Phase 4: Verify

Prove — with fresh evidence, not recollection — that the implementation does
what the design and specs say. **Evidence before assertions, always.**
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry.

## Entry check

- `onto state <name> --json` reports recorded phase or dispatcher-routed derived
  phase `verify`, and every `tasks.md` item is checked (items explicitly marked
  deferred-to-close are allowed).
- Read `notes.md` at entry when present — accepted decisions and recorded
  directives inform what to verify against.
- Unchecked tasks mean build isn't done — the dispatcher's derivation table
  will send this back to build; route through `/onto`.
- On a downward mismatch from close, replace the invalidated report with fresh
  evidence but leave the recorded close phase unchanged; return through `/onto`
  after recording the result.

## Steps

### 1. Scale check → verification mode

**Measure first, then apply the risk override.** Run `onto scale <name>` — it
measures the `base_ref..HEAD` diff (non-test file count, changed lines) and
derives `light`/`full` from size (the same >5-non-test-file threshold the preset
upgrade gates use). That is the *measured* floor. Then **upgrade to full** on any
risk trigger below regardless of what the measurement said, and record the final
level with `onto set verify-scale <name> light|full` (or `onto scale <name>
--set` when size alone decides):

- **full** — `workflow: full`, any upgraded preset, the measured size is `full`,
  a new capability, **or a diff touching a security-sensitive surface** — secret
  resolution, remote fetch/verify, file deletion/pruning, or permission/ownership
  — regardless of file count. Scale keys on risk, not just size: a one-file
  security change is never under-scrutinized. Checks every delta-spec scenario,
  the full design, and the regression suite.
- **light** — a preset within its limits (≤5 non-test files, by
  construction under the upgrade gates) **and touching no security-sensitive
  surface** (else full applies). Checks the changed behavior's scenarios plus
  the regression suite; the report may be brief but never absent.

### 2. Check against design and specs

For **every scenario in every delta spec** (workspace `specs/*.md`): obtain
candidate-bound command evidence under the shared
[execution policy](../homonto/references/execution.md#candidate-bound-verification).
Reuse a matching observed run when its provenance and inputs are unchanged;
otherwise run the command(s) and capture the actual output.
Walk `design.md`'s key decisions and confirm the implementation matches —
deviations are findings, not footnotes. Re-run stated verifications from
`plan.md` where they are cheap.

For presets without design/plan/deltas, verify every proposal Acceptance Scenario
and the inline tasks' Verify contracts. No deltas does not mean zero scenarios;
include the fix reproduction or tweak's observable result and regression cases.

**Fan out the analysis, centralize execution.** With more than a handful of
scenarios, dispatch `onto-explorer` agents concurrently, one per capability or
related group, to map each claim to implementation and propose exact evidence
commands. Explorers have no shell so they cannot race or mutate the candidate.
The orchestrator runs those commands, captures literal output, and drafts the
evidence table. Keep command execution serial when scenarios share fixtures, a
port, or a database.

Rules of evidence:

Use each selected source alias's execution root and immutable `repo_bases`
anchor, not configRoot or records HEAD. For scenario receipts, use the implemented
`onto evidence record <name> --repo <alias> --task <trace-id> --scenario <id> --exec <executable> --cmd-hash <sha256> --exit <status> --output <absolute-output-file> --dir "<configRoot>"`.
Run commands first and retain their actual results; do not record receipts yet.
Step 4 finalizes the report before hashing it into receipts. `onto set verify-result
<name> pass --dir "<configRoot>"` binds all selected source HEADs. Preserved dirt
is not a gate waiver; use validated registered bindings, not a clean arbitrary cwd.

- Every claim needs a candidate-bound command + its literal output. A bare
  "passed earlier" is not reusable evidence; validate provenance and coverage.
- A scenario that cannot be demonstrated is a **fail**, not a skip.

### 3. Regression

Obtain the project's full build and test suite results for the final candidate.
Run missing or invalidated checks; do not repeat an unchanged build-exit run
solely because the phase changed. Retain the output. If the
project has no build/test suite (e.g. a content-only repo), record that
fact as the regression result. It is a valid result, not a skipped check.

### 3b. Adversarial pass

After the self-evidence table and regression results are ready, supply the
complete evidence pack to skeptics. Follow
`references/adversarial.md`: **full mode requires two parallel
fresh-context skeptics** — dispatch the **`onto-skeptic`** subagent twice at
once, naming one lens per dispatch: conformance (refute each scenario claim)
and robustness (edge cases, drift/recovery paths). Two is the floor, not the
ceiling, and there are two ways to add: a large full verify (many scenarios,
several capabilities) MAY shard the conformance lens across additional
skeptics — one per capability, each dispatch naming its capability's scenarios
— while robustness stays one; and a change may earn an extra **lens**
(abuse, data/migration, compatibility) per `references/adversarial.md` — those
names differ from build's reviewer lenses on purpose, because a skeptic attacks
the running system, not the diff. All deny edits and shell commands, so they go
in the same parallel batch without mutating the candidate. Both mandatory lenses are prompted to
refute, never approve; light mode uses one optional skeptic with skips
recorded. Triage
findings per the protocol: a refuted claim fails its scenario; new defects
are CRITICAL-fix or gate-decided deviations. **Non-waivable classes:** a
security defect, data loss, or a failed core-acceptance scenario is CRITICAL
and must be fixed — it is never waived, skipped, or gate-accepted as a
deviation, in light or full mode. Only lower-severity findings are eligible
for a recorded deviation. For unavailable dispatch apply the shared execution
policy: perform the claim/gap checks directly and record the missing independent
pass in Adversarial. An explicit independent-review requirement remains a blocker;
denial and blocked workers are not absence. Direct review never counts as an
independent skeptic, and surfaced non-waivable defects still block.

### 4. Write the report

Write `<workflow-root>/changes/<name>/verification.md` from the canonical template
`references/verification.md` (header with machine-read `Result:` line,
scenario-evidence table, design conformance, adversarial pass, regression,
deviations). When deviations were accepted, the Result line carries their
count — `Result: pass (2 accepted deviations)` — so a pass with caveats is
visibly different from a clean one everywhere the line is read. Record the
result only after this ordering: finish scenario/adversarial/regression evidence
and the no-slop edit, finalize `verification.md`, checkpoint its manual edits in
managed mode, then record each current scenario receipt with `--artifact
<absolute-verification.md>`, then `onto set verify-result <name> pass|fail`.
Do not edit the report after receipts: their artifact hashes would be stale.
If a finding changes the candidate or report, run a fresh round and record every
current claim again using `onto evidence record`. The latest record for the same
repository/task/scenario supersedes earlier claims without deleting audit history;
there is no separate round-reset command. Re-record all claims bound to a changed
report, not only the previously failing scenario. Never hand-edit the sidecar.
For no-spec fix/tweak changes, declare stable `Scenario-ID: <id>` lines in
`tasks.md` or `verification.md` and use those IDs in receipts. Do not invent delta
specs merely to satisfy evidence lookup. Inspect `onto doctor` / `onto trace`
after recording to confirm current claims and resolve stale or unknown-ID findings.

### 5. Failure handling

On any failure, record `onto set verify-result <name> fail` once per round, which increments
`observed.verify_rounds`, and note the date and failing items in `notes.md`.
If step 4 already recorded fail, do not increment it again here.
Default to **fix**: verify each finding against the candidate, then turn every
real in-scope source defect into an unchecked repair task before changing code.
Invalidate a previous passing report (`Result: superseded (repair <date>)`) and
record pending unless this round already recorded fail. Preserve the recorded
phase; unchecked tasks route back to build. Select the repair route by workflow:

| Workflow | Repair contract and route |
|---|---|
| full | Append a matching `plan.md` detail block with Owner/Repo/Cwd/Files/Change/Verify; load `onto-build` and continue in the same invocation |
| fix | Append the inline contract to `tasks.md`; load `onto-fix` step 2, retaining failing-test-first execution |
| tweak | Append the inline contract to `tasks.md`; load `onto-tweak` step 2 |

Do not create a plan or design for a preset repair. `onto-build` re-dispatches
`onto-implementer` when its recorded mode and actual capability permit it; records
tasks remain coordinator-owned. Return here for fresh verification of the changed
candidate. Do not stop at a skeptic finding or ask whether to continue a clear
in-scope repair. Escalation follows the shared eligibility table, not failure alone.

Ask the user only if accepting a known lower-severity deviation is a real option
and fixing it would cross a user-owned constraint. Never recommend acceptance,
auto-accept it, or offer it for security defects, data loss, or failed core
acceptance scenarios. If authorized, record each deviation and rationale in
`verification.md`, keep `Result: pass (N accepted deviations)`, and set the
recorded result to pass. After three failed rounds, use fresh investigation and
replanning; the count is a warning, not a mandatory user interruption.

## Exit checklist

- [ ] `verification.md` exists with a `Result:` line and fresh evidence for
      every checked scenario, regression results included
- [ ] `verify.result: pass` recorded via `onto set verify-result <name> pass`
      and in the report (accepted deviations, if any, each recorded with
      rationale in the report)
- [ ] Adversarial pass run (or its skip recorded in the report's
      Adversarial section)
- [ ] Report prose edited for clarity before receipt hashing; preserve the
      machine-read `Result:` line and literal evidence; no style receipt required
- [ ] **Record the workspace**: managed Markdown uses
      `homonto workspace checkpoint --path changes/<name> --message "Record verification"`
      before state mutations; binary evidence/result writes checkpoint
      automatically. Existing mode retains named manual records commits.
- [ ] If recorded phase is verify, advanced verify → close via `onto advance
       <name>`; on a downward mismatch, skipped advance and returned to `/onto`
- [ ] The phase-state update is recorded before close: automatic checkpoint in
      managed mode, named manual commit in existing mode
- [ ] Load `onto-close` and continue in the same invocation unless the user
      named verify as the endpoint or asked to pause
