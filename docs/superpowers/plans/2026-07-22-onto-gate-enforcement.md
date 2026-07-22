---
change: onto-gate-enforcement
design-doc: docs/superpowers/specs/2026-07-22-onto-gate-enforcement-design.md
base-ref: fcfc078c3c575a98054062363e865c291577143b
---

# onto-gate-enforcement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans
> to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Evidence tokens for the judgment gates, a verify-report cross-check,
truthful binary phase derivation, and a one-call preset advance.

**Architecture:** Extend `internal/ontostate` (three gated-core fields +
`DeriveWorkingPhase`), enforce in `internal/ontocli` (advance, mergedeltas,
close), surface in set/gate/state commands, then update the catalog skills
that ask the gates. Every gate is a refuse/pass pair with a test on each side.

**Tech Stack:** Go 1.26, cobra, yaml.v3; catalog markdown.

## Global Constraints

- Evidence tokens are free-form non-empty strings (convention:
  `YYYY-MM-DD <summary>`); the binary checks presence, never judges content (B1).
- Presets (`fix`/`tweak`) are exempt from `proposal_approved` /
  `approach_confirmed`; nothing is exempt from `close_confirmed`.
- No `SchemaVersion` bump — absent fields decode as empty.
- Every refusal message names the `onto set` command that unblocks it.
- TDD: failing test first for each gate; `go test ./internal/onto... ` after
  each task; one commit per task.

---

### Task 1: State fields

**Files:**
- Modify: `internal/ontostate/state.go` (State struct, gated core)
- Test: `internal/ontostate/state_test.go`

**Interfaces:**
- Produces: `State.ProposalApproved`, `State.ApproachConfirmed`,
  `State.CloseConfirmed` (all `string`).

- [x] **Step 1:** Failing test: round-trip the three fields through
  Marshal/Parse; assert a state without them parses with empty strings.
- [x] **Step 2:** Add the three fields after `Directive` in the struct with
  `yaml:"…,omitempty" json:"…,omitempty"` tags. Run tests → pass.
- [x] **Step 3:** Commit `feat(ontostate): evidence-token fields`.

### Task 2: proposal_approved + approach_confirmed advance gates

**Files:**
- Modify: `internal/ontocli/advance.go` (phase-evidence gate block)
- Test: `internal/ontocli/advance_test.go`

**Interfaces:**
- Consumes: Task 1 fields.

- [x] **Step 1:** Failing tests: (a) full change at open with artifacts
  present refuses advance naming `proposal-approved`; (b) same change with
  the field set advances; (c) fix preset at open advances without it;
  (d) full change at design with isolation set but no `approach_confirmed`
  refuses naming `approach-confirmed`; (e) set → advances; (f) preset exempt.
- [x] **Step 2:** In `runAdvance`, in the evidence-gate section:

```go
full := st.Workflow == "" || st.Workflow == "full"
if st.Phase == "open" && full && st.ProposalApproved == "" {
    return fmt.Errorf("onto advance: cannot leave open: proposal approval not recorded (run `onto set proposal-approved %s \"<evidence>\"` after the artifact-review gate)", name)
}
if next == "build" && full && st.ApproachConfirmed == "" {
    return fmt.Errorf("onto advance: cannot enter build: approach confirmation not recorded (run `onto set approach-confirmed %s \"<evidence>\"` after the approach gate)", name)
}
```

- [x] **Step 3:** Tests pass; commit `feat(onto): judgment-gate evidence tokens gate advance`.

### Task 3: verify cross-check

**Files:**
- Modify: `internal/ontocli/advance.go`
- Test: `internal/ontocli/advance_test.go`

- [x] **Step 1:** Failing tests: leaving verify with `verify.result: pass` but
  (a) `verification.md` `Result: fail` refuses naming both; (b)
  `Result: pass (2 accepted deviations)` advances; (c) no `Result:` line
  refuses; (d) unreadable file refuses.
- [x] **Step 2:** After the existing verify.result check:

```go
if st.Phase == "verify" {
    line, err := verificationResultLine(filepath.Join(changeDir, "verification.md"))
    if err != nil {
        return fmt.Errorf("onto advance: cannot leave verify: %w", err)
    }
    if !strings.HasPrefix(line, "Result: pass") {
        return fmt.Errorf("onto advance: cannot leave verify: verification.md says %q but verify.result=pass — the report and the state must agree", line)
    }
}
```

with `verificationResultLine` scanning for the first `Result: `-prefixed line
(error when absent).
- [x] **Step 3:** Tests pass; commit `feat(onto): cross-check verification.md at the verify exit`.

### Task 4: close_confirmed enforcement

**Files:**
- Modify: `internal/ontocli/mergedeltas.go`, `internal/ontocli/close.go`
- Test: `internal/ontocli/mergedeltas_test.go`, `internal/ontocli/close_test.go`

- [x] **Step 1:** Failing tests: merge-deltas and close each refuse with
  empty `close_confirmed` (message names `onto set close-confirmed`), and the
  refusal happens before any file mutation (assert target spec untouched).
- [x] **Step 2:** First check after load+validate in both commands:

```go
if st.CloseConfirmed == "" {
    return fmt.Errorf("onto %s: close not confirmed: the final-confirmation gate must be answered first (run `onto set close-confirmed %s \"<evidence>\"`)", cmdName, name)
}
```

- [x] **Step 3:** Tests pass; commit `feat(onto): close-confirmed token gates global mutation`.

### Task 5: setters

**Files:**
- Modify: `internal/ontocli/set.go`
- Test: `internal/ontocli/set_test.go`

**Interfaces:**
- Produces: `onto set proposal-approved|approach-confirmed|close-confirmed
  <change> <evidence>`.

- [ ] **Step 1:** Failing tests: each setter stores its value; empty evidence
  refused.
- [ ] **Step 2:** `evidenceSetterCmd(field string, assign func(*State, string))`
  on the `directiveCmd` pattern; register the three.
- [ ] **Step 3:** Tests pass; commit `feat(onto): evidence-token setters`.

### Task 6: pendingGates

**Files:**
- Modify: `internal/ontocli/gate.go`
- Test: `internal/ontocli/gate_test.go`

- [ ] **Step 1:** Failing tests: open/full lists `proposal-approved`;
  design/full lists `approach-confirmed` before `isolation`; close lists
  `close-confirmed` first; fix preset lists neither full-only token; answered
  tokens disappear.
- [ ] **Step 2:** Add the three `pendingGate` entries (no fixed options;
  SetCommand shows the evidence argument).
- [ ] **Step 3:** Tests pass; commit `feat(onto): judgment gates in onto gate --json`.

### Task 7: DeriveWorkingPhase

**Files:**
- Create: `internal/ontostate/derive.go`
- Test: `internal/ontostate/derive_test.go`

**Interfaces:**
- Produces: `func DeriveWorkingPhase(changeDir string, st State) string`
  (never errors; missing files = absent evidence).

- [ ] **Step 1:** Failing tests, one per table row: archived→done;
  `Status: Under revision`→design; `Result: pass`→close; all-checked
  tasks→verify; `Status: Confirmed`→build; preset workspace→build; full
  incomplete-tasks→design; full unconfirmed design→design; full
  proposal-only + claimed open→open, claimed design→design; missing
  proposal→open.
- [ ] **Step 2:** Implement with a line-prefix scanner (`fileHasLinePrefix`)
  and the existing `TasksAllChecked`; first match wins, exactly the design
  order.
- [ ] **Step 3:** Tests pass; commit `feat(ontostate): artifact-based working-phase derivation`.

### Task 8: truthful state output

**Files:**
- Modify: `internal/ontocli/statecmd.go`, `internal/ontocli/status.go`
- Test: `internal/ontocli/statecmd_test.go`, `internal/ontocli/status_test.go`

- [ ] **Step 1:** Failing tests: `onto state --json` on a confirmed-design
  workspace reports `derived_phase: build` + `phase_mismatch` true when
  claimed differs; `onto status` row shows ` (working: build)` on mismatch.
- [ ] **Step 2:** Wire `DeriveWorkingPhase` into both; keep claimed `phase`
  field untouched.
- [ ] **Step 3:** Tests pass; commit `feat(onto): state/status report the derived working phase`.

### Task 9: advance --to build

**Files:**
- Modify: `internal/ontocli/advance.go`
- Test: `internal/ontocli/advance_test.go`

- [ ] **Step 1:** Failing tests: fix preset at open with isolation set
  reaches build in one call; full refused (`advance one gate at a time`);
  `--to verify` refused; a failing hop (missing tasks.md) surfaces that
  hop's error.
- [ ] **Step 2:** `--to` flag; loop single hops reloading state until
  `build`, preset-only.
- [ ] **Step 3:** Tests pass; commit `feat(onto): one-call gated preset advance`.

### Task 10: skills + guide + version

**Files:**
- Modify: `catalog/skills/onto-open/SKILL.md`, `onto-design/SKILL.md`,
  `onto-close/SKILL.md`, `onto-fix/SKILL.md`, `onto-tweak/SKILL.md`,
  `catalog/skills/onto/SKILL.md`, `docs/guides/onto-workflow.md`,
  `catalog/frameworks/onto/framework.toml`

- [ ] **Step 1:** Gate blocks record their tokens (open artifact-review →
  `proposal-approved`; design approach → `approach-confirmed`; close final →
  `close-confirmed`); exit checklists updated; fix/tweak use
  `onto advance <name> --to build`; dispatcher §3 consumes
  `onto state <name> --json` derivation; guide gate table + migration note;
  framework 0.5.0 → 0.6.0.
- [ ] **Step 2:** Commit `feat(onto-skills): record evidence tokens at the gates`.

### Task 11: full verification

- [ ] **Step 1:** `go build ./... && go vet ./... && go test ./...` — all green.
- [ ] **Step 2:** Extend `test/docker/onto-lifecycle.sh`: missing-token
  advance refusal, `--to build` leg for a preset, verify cross-check refusal.
  Run the suite (or the full docker gate) and paste results.
- [ ] **Step 3:** Commit `test(e2e): onto-lifecycle exercises evidence gates`.
