---
name: onto-verify
description: onto phase 4 — verify. Use when an active change has phase verify (all tasks checked) — picks a verification level from change scale, checks implementation against design and every spec scenario with fresh evidence, and writes verification.md.
---

# onto-verify — Phase 4: Verify

Prove — with fresh evidence, not recollection — that the implementation does
what the design and specs say. **Evidence before assertions, always.**
Apply the dispatcher's shared autonomous workflow policy throughout.

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

For **every scenario in every delta spec** (workspace `specs/*.md`): run the
command(s) that demonstrate the behavior and capture the actual output.
Walk `design.md`'s key decisions and confirm the implementation matches —
deviations are findings, not footnotes. Re-run stated verifications from
`plan.md` where they are cheap.

**Fan out the analysis, centralize execution.** With more than a handful of
scenarios, dispatch `onto-explorer` agents concurrently, one per capability or
related group, to map each claim to implementation and propose exact evidence
commands. Explorers have no shell so they cannot race or mutate the candidate.
The orchestrator runs those commands, captures literal output, and drafts the
evidence table. Keep command execution serial when scenarios share fixtures, a
port, or a database.

Rules of evidence:

- Every claim needs a fresh command + its literal output. No "should work",
  no "passed earlier", no stale logs.
- A scenario that cannot be demonstrated is a **fail**, not a skip.

### 2b. Adversarial pass

After the self-evidence table is drafted, follow
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
for a recorded skip. No dispatch capability → record the skipped pass in the
report's Adversarial section (protocol-mandated skips live there, no acceptor
needed) — but a non-waivable-class finding already surfaced still blocks.

### 3. Regression

Run the project's full build and test suite. Capture the output. If the
project has no build/test suite (e.g. a content-only repo), record that
fact as the regression result — that is a valid result, not a skipped
check.

### 4. Write the report

Write `<workflow-root>/changes/<name>/verification.md` from the canonical template
`references/verification.md` (header with machine-read `Result:` line,
scenario-evidence table, design conformance, adversarial pass, regression,
deviations). When deviations were accepted, the Result line carries their
count — `Result: pass (2 accepted deviations)` — so a pass with caveats is
visibly different from a clean one everywhere the line is read. Record the
result via `onto set verify-result <name> pass|fail`.

### 5. Failure handling

On any failure, record `onto set verify-result <name> fail`, which increments
`observed.verify_rounds`, and note the date and failing items in `notes.md`.
Default to **fix**: add tasks for the failures in `tasks.md`; the unchecked tasks
drive derivation back to build without a backward phase write. Repair, then run a
fresh verification round.

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
- [ ] onto-no-slop pass run over `verification.md`, recorded in
      `notes.md` (`no-slop: verification done`) — never touch the
      machine-read `Result:` line or the evidence table structure
- [ ] If recorded phase is verify, advanced verify → close via `onto advance
      <name>`; on a downward mismatch, skipped advance and returned to `/onto`
- [ ] **Commit the workspace**: `git add <workflow-root>/changes/<name> && git commit`
      — every phase exits with its workspace committed
- [ ] Load `onto-close` and continue in the same invocation unless the user
      named verify as the endpoint or asked to pause
