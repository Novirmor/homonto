# onto-gate-enforcement — tasks

## 1. State fields (D1 foundation)

- [ ] 1.1 `internal/ontostate`: add `proposal_approved`, `approach_confirmed`,
  `close_confirmed` (strings, omitempty) + Validate passthrough; tests for
  load/save round-trip and empty-decode compatibility.

## 2. Advance gates (D1 + D2)

- [ ] 2.1 `advance open→design`: full-only `proposal_approved` refusal naming
  the `onto set` command; preset unaffected. Tests both.
- [ ] 2.2 `advance design→build`: full-only `approach_confirmed` refusal
  (alongside isolation). Tests: missing token refuses, present advances.
- [ ] 2.3 `advance verify→close`: cross-check `verification.md` `^Result: pass`
  prefix against `verify.result`; disagreement/unreadable refuses naming both.
  Tests: fail-vs-pass mismatch, accepted-deviations suffix passes, missing
  file refuses.

## 3. Close enforcement (D1)

- [ ] 3.1 `merge-deltas` + `close`: refuse on empty `close_confirmed` before
  any mutation. Tests: refusal is pre-mutation (no spec file touched).

## 4. Set + gate surfaces (D1)

- [ ] 4.1 `onto set proposal-approved|approach-confirmed|close-confirmed`
  accepting a free-form evidence value. Tests.
- [ ] 4.2 `pendingGates`: the three new gates in phase order with questions,
  recommended options, set commands; full-only visibility for the two design
  tokens. Tests.

## 5. Derivation (D3)

- [ ] 5.1 `ontostate.DerivePhase(changeDir, st)`: evidence table, first match
  wins; unit tests per table row incl. the open↔design claimed-phase rule and
  preset build default.
- [ ] 5.2 `onto status --json`: add `derived_phase` + `phase_mismatch`.
  Tests: confirmed-design→build, downward mismatch flagged.

## 6. Preset advance (D4)

- [ ] 6.1 `onto advance --to build`: preset-only multi-hop, per-hop gates run,
  refuses for full and for other targets. Tests: fix reaches build in one
  call; full refused; hop failure surfaces the failing gate.

## 7. Skills + docs

- [ ] 7.1 `onto-open/SKILL.md`: artifact-review gate records
  `onto set proposal-approved`; exit checklist updated.
- [ ] 7.2 `onto-design/SKILL.md`: approach gate records
  `onto set approach-confirmed`; exit checklist updated.
- [ ] 7.3 `onto-close/SKILL.md`: final gate records `onto set close-confirmed`
  before merge-deltas; checklist updated.
- [ ] 7.4 `onto-fix` + `onto-tweak`: replace the scripted double-advance with
  `onto advance <name> --to build`.
- [ ] 7.5 `onto/SKILL.md` §3: consume `derived_phase`/`phase_mismatch` from
  `onto status --json`; keep the routing rules, drop the hand-run table to a
  reference of what the binary computes.
- [ ] 7.6 `docs/guides/onto-workflow.md`: gate table gains the three tokens +
  the in-flight-change migration note; framework.toml 0.5.0 → 0.6.0.

## 8. Verification

- [ ] 8.1 `go build ./... && go vet ./...` clean; `go test ./...` green.
- [ ] 8.2 E2E: extend `test/docker/onto-lifecycle.sh` — the advance-refusal
  path for a missing `approach_confirmed`, the `--to build` preset leg (if it
  exists in the suite), and the verify cross-check refusal.
