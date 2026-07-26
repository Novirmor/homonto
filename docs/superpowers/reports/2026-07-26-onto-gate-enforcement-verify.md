# Verification Report: onto-gate-enforcement

- **Date:** 2026-07-26
- **Mode:** full (18 tasks, 39 changed files, 1 delta capability)
- **Branch:** `feature/20260722/onto-gate-enforcement` (16 commits)
- **Base ref:** `8c6f3a0` (`chore: archive onto-catalog-hygiene`)
- **Delta spec:** `openspec/changes/onto-gate-enforcement/specs/onto-evidence-gates/spec.md`

## Summary

| Dimension    | Status                                                          |
|--------------|-----------------------------------------------------------------|
| Completeness | 18/18 tasks checked; 32/32 plan steps; 4/4 delta requirements    |
| Correctness  | 10/10 delta scenarios covered by code + tests; 5/5 E2E suites    |
| Coherence    | D1–D5 followed; three wording/shape divergences recorded below   |

**Assessment:** No CRITICAL issues. The standard-mode whole-change review's
three Important findings are fixed with regression tests (`55c706e`). Ready
for archive.

## Evidence (fresh runs, 2026-07-26)

| Check       | Command                                | Result                          |
|-------------|----------------------------------------|---------------------------------|
| Build       | `go build ./...`                       | Success                         |
| Vet         | `go vet ./...`                         | No issues                       |
| Format      | `gofmt -l internal/`                   | empty                           |
| Tests       | `go test ./...`                        | 1002 passed, 42 packages        |
| Gate tests  | `go test ./internal/ontocli/... ./internal/ontostate/... -count=1` | 253 passed, 2 packages |
| Docker E2E  | `./scripts/docker-test.sh`             | 5/5 suites PASS (exit 0)        |

The E2E run is post-review-fix: `advance.go` changed in `55c706e`, so the
suites were re-run after that commit rather than relying on the pre-review
pass. Suites: homonto-core, homonto-expanded, onto-lifecycle, to-lifecycle,
release-packaging.

## Completeness

- `openspec/changes/onto-gate-enforcement/tasks.md`: 18/18 `[x]`, none deferred.
- `docs/superpowers/plans/2026-07-22-onto-gate-enforcement.md`: all 32 steps checked.
- Delta spec: all 4 requirements implemented in the binary, not only in prose.
- `catalog/frameworks/onto/framework.toml` 0.5.0 → 0.6.0; the six skills that
  own the gates (`onto`, `onto-open`, `onto-design`, `onto-close`, `onto-fix`,
  `onto-tweak`) and `docs/guides/onto-workflow.md` all reference the new
  surfaces.

## Correctness — scenario coverage

### Requirement: Judgment gates require recorded evidence tokens

| Scenario | Evidence |
|----------|----------|
| Full change cannot leave design unconfirmed | `TestAdvanceCommand_EnteringBuildRequiresApproachConfirmed`; E2E `onto-lifecycle.sh:79` |
| Token recorded — advance proceeds | same test's pass leg; E2E `onto-lifecycle.sh:80–83` |
| Close refuses unconfirmed global mutation | `TestMergeDeltasCommand_RefusesWithoutCloseConfirmed` (asserts no spec file touched), `TestCloseCommand_RefusedWithoutCloseConfirmed`; E2E `onto-lifecycle.sh:100` |
| Presets skip the full-only tokens | `TestAdvanceCommand_PresetLeavesOpenWithoutProposalApproved` |

Supporting: open exit gated by `TestAdvanceCommand_LeavingOpenRequiresProposalApproved`
(E2E `:59`); setters by `TestSetEvidenceTokens`; `onto gate --json` visibility
and phase ordering by `TestPendingGates_ByPhaseAndState` + `TestGateCommand_JSONAndHuman`;
state persistence by `TestEvidenceTokens_RoundTripAndAbsentDecode` (absent
fields decode empty — no schema bump needed) and
`TestMarshalParse_RoundTrip_PreservesEveryGatedField`.

### Requirement: Verify result is cross-checked against the report

| Scenario | Evidence |
|----------|----------|
| State says pass, report says fail | `TestAdvanceCommand_LeavingVerifyCrossChecksReport`; E2E `onto-lifecycle.sh:86–89` |
| Accepted deviations still pass | `TestVerificationResultLine_SharedScannerSemantics/deviations_suffix`; cross-check test's pass leg |

`TestVerificationResultLine_SharedScannerSemantics` pins the scanner semantics
both the gate and the derivation depend on: indented `Result:` accepted, the
FIRST `Result:` line wins over a later pass, and `Result: passing` is not a
pass. Missing file / missing `Result:` line refusals stay covered by
`TestAdvanceCommand_LeavingVerifyBlockedWithoutPass`.

### Requirement: The binary derives the working phase from artifacts

| Scenario | Evidence |
|----------|----------|
| Confirmed design derives build | `TestDeriveWorkingPhase_EvidenceTable/confirmed_design_derives_build`; `TestStateJSON_EmitsFullStateAndDerivedPhase` |
| Downward mismatch is flagged | `TestStateJSON_DerivedPhaseIsArtifactBased` (claim `verify` → derived `design`, `phase_mismatch: true`); `TestStatusCommand_FlagsWorkingPhaseMismatch` (`(working: design)`) |

`TestDeriveWorkingPhase_EvidenceTable` covers all 8 evidence-table rows plus
the first-match-wins precedence (12 cases: archived over everything, under-revision
over confirmed evidence, preset workspace default, the open↔design claimed-phase
rule, and proposal-missing → open). The "SHALL NOT merely echo the claimed
phase" clause is what `TestStateJSON_DerivedPhaseIsArtifactBased` exists to
prove.

### Requirement: Presets advance to build in one gated call

| Scenario | Evidence |
|----------|----------|
| Fix preset reaches build in one call | `TestAdvanceCommand_ToBuildPresetOneCall`; E2E `onto-lifecycle.sh:142` |
| Full workflow refused | `TestAdvanceCommand_ToBuildRefusalCases` (full workflow, non-build target, mid-hop gate failure) |

Hardened by the review: `TestAdvanceCommand_ToBuildValidatesBeforeRead` (gate +
`ValidChangeName` precede any path construction) and
`TestAdvanceCommand_ToBuildRefusesAtOrPastBuild` (asserts the state file is not
mutated when the change is already at build or verify).

## Coherence — design adherence

- **D1 (evidence tokens):** followed. Fields are free-form strings in the gated
  core with no shape validation (B1: presence, not judgment); refusals name the
  token and the `onto set` command; presets exempt from the two full-only tokens.
- **D2 (verify cross-check):** followed, then tightened. D2 specified a
  `Result: pass` *prefix* match; the shipped `ResultLineIsPass` requires the
  exact word `pass` (suffix `(N accepted deviations)` still accepted), so
  `Result: passing` no longer reads as a pass. Strictly stronger than designed
  and consistent with the delta spec's intent.
- **D3 (truthful derivation):** followed, with a signature shape change —
  `DeriveWorkingPhase(changeDir string, st State) string`, not the designed
  `(string, error)`. D3's own prose says missing files are "absent", not
  errors, so there was no error condition left to return; the abandoned-change
  case echoes the claim rather than deriving a live phase.
- **D4 (preset `--to build`):** followed. Single-hop `runAdvance` in a loop with
  state reloaded per hop, so every per-hop gate fires and a failing hop's error
  propagates unchanged.
- **D5 (skills + docs):** followed. The dispatcher's evidence table is retained
  as documentation of what the binary computes, explicitly marked as such.

## Review and its fixes

A `review_mode: standard` whole-change review (dispatched subagent) raised 3
Important and 5 minor findings. All are resolved in `55c706e`, each with a
regression test that fails against the prior commit:

1. `runAdvanceTo` built a path from the change name before validating it —
   fixed by moving `ontoFramework.Gate` + `ValidChangeName` to the command
   entry, restoring the traversal-entry invariant every other onto command holds.
2. `--to build` would walk a change already at or past build, mutating state it
   was not asked to touch — now refused.
3. The verify gate and the derivation each had their own `Result:` scanner and
   could disagree — collapsed into one shared
   `ontostate.VerificationResultLine` + `ResultLineIsPass`; the private
   `verificationResultLine` in `advance.go` is deleted.

Minors: `onto state` text output now leads with the claimed phase and annotates
`(working: X)` on mismatch (mirroring `onto status`) instead of silently
substituting the derivation; `DeriveWorkingPhase` echoes the claim for an
abandoned change; the `merge-deltas` `close_confirmed` check moved *after* the
idempotent already-merged no-op, so an interrupted close can finish — a
deliberate divergence from D1's "first check after state load", since an
already-merged change has nothing left to confirm. Review minor 8 (a comment
wording nit on evidence-table row 6) was not applied.

## Notes and gaps

- **Task-text drift, no functional gap:** task 5.2 says "`onto status --json`:
  add `derived_phase` + `phase_mismatch`". `onto status` has no `--json` flag;
  the fields ship on `onto state <name> --json`, which is what the canonical
  delta spec requires and what the dispatcher skill consumes. `onto status`
  carries the derivation as the `(working: X)` text annotation. The canonical
  spec is satisfied; the task sentence is stale.
- **Derivation coverage is unit-level, not E2E.** Task 8.2 asked for three E2E
  legs (missing-token refusal, `--to build`, verify cross-check) and all three
  are in `test/docker/onto-lifecycle.sh`. `derived_phase` / `phase_mismatch`
  are exercised through the real command surface in Go tests
  (`runOnto("state", …, "--json")`) but have no Docker leg. Low risk; noted
  rather than expanded.
- **In-flight full changes block on their next advance** until the skill
  records the missing token. Intended (the unanswered gate is re-asked) and
  documented in `docs/guides/onto-workflow.md`'s migration note, which names
  v0.9.0 as the release that introduces it — `catalog/version.txt` is still
  0.8.0, so the note is forward-looking until the release train runs.
