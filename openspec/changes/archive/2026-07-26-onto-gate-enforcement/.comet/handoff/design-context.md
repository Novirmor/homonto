# Comet Design Handoff

- Change: onto-gate-enforcement
- Phase: design
- Mode: compact
- Context hash: 56b1329856623fea473c86f5b467388ca2d51a0dd7f146a0bb153401b7f78ea5

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## openspec/changes/onto-gate-enforcement/proposal.md

- Source: openspec/changes/onto-gate-enforcement/proposal.md
- Lines: 1-70
- SHA256: f0a5e9d02371d86460b29c0968a8a404fffc8b2a837711e54d844c0feedcc1ba

```md
# onto-gate-enforcement

## Why

The 2026-07-22 critique of the onto flow found the enforcement story
half-finished. `onto gate --json` covers the bookkeeping decisions
(isolation, build/tdd-mode, verify-result, close-merged, guides,
integration) while the judgment gates that actually protect the user —
proposal approval, approach confirmation, close confirmation — are prose-only
`> **GATE:**` blocks recorded, at best, as freeform notes. The framework's
own doctrine (B1: an honest agent cannot skip a step; ceremony, not
judgment) is implemented for `tdd-mode` and not for "did the user confirm
the approach before design.md was written."

Three adjacent defects share the fix surface: `verify-result` is
self-asserted (state can say `pass` while `verification.md` says
`Result: fail` and `onto advance` still promotes); phase derivation — the
most load-bearing logic in the framework — is a prose table an LLM re-executes
per dispatch instead of tested Go; and the presets instruct agents to burn
two ceremonial `onto advance` calls, training the exact gate-skipping muscle
the rest of the framework forbids.

## What Changes

- **Evidence tokens for the judgment gates.** New gated-core state fields:
  `proposal_approved` (full: required to leave open), `approach_confirmed`
  (full: required to leave design, alongside isolation), `close_confirmed`
  (all workflows: required by `onto merge-deltas` and `onto close` before any
  global mutation). Each recorded via `onto set …`, surfaced in
  `onto gate --json`, enforced by `onto advance` / the close commands.
- **Verify cross-check.** `onto advance` out of verify refuses unless
  `verification.md` carries a `Result: pass` line agreeing with
  `verify.result` in state.
- **Binary phase derivation.** `onto status --json` gains `derived_phase`,
  computed from workspace artifacts by the dispatcher's evidence table, now
  ported to tested Go. The dispatcher skill consumes it instead of
  re-deriving by hand.
- **Preset advance sugar.** `onto advance <name> --to build` performs the
  gated open→design→build sequence in one call for preset workflows and
  refuses past build (and refuses entirely for `workflow: full`). The
  fix/tweak skills stop scripting two hollow advances.
- **Skills updated** to record the new tokens at their gates (onto-open,
  onto-design, onto-close), use `--to build` (onto-fix, onto-tweak), and
  consume `derived_phase` (onto dispatcher). onto framework version bump.

## Capabilities

### New Capabilities

- `onto-evidence-gates`: which workflow decisions require recorded evidence
  tokens, which commands refuse without them, the verify-report cross-check,
  binary phase derivation, and the preset advance path.

### Modified Capabilities

None existing (`openspec/specs/` holds only `agent-models`).

## Impact

- Go: `internal/ontostate` (fields, validation, derivation),
  `internal/ontocli` (advance, set, gate, status, mergedeltas, close, new
  tests).
- Catalog: `onto/SKILL.md` (§3 consumes derived_phase), `onto-open`,
  `onto-design`, `onto-close`, `onto-fix`, `onto-tweak`;
  `catalog/frameworks/onto/framework.toml` 0.5.0 → 0.6.0.
- **Breaking for in-flight full changes**: an existing change mid-design has
  no `approach_confirmed`; its next `onto advance` refuses until the token is
  recorded (the skill asks the gate — which is the point). Documented in the
  guide.
- Docs: `docs/guides/onto-workflow.md` gate table.

```

## openspec/changes/onto-gate-enforcement/design.md

- Source: openspec/changes/onto-gate-enforcement/design.md
- Lines: 1-54
- SHA256: f5673474f5f7b229d83858a51f0bef3b049290f3d9b3dca15ea38283f83640f9

```md
# onto-gate-enforcement — design (high level)

The deep technical design lives in the design-phase Design Doc
(`docs/superpowers/specs/2026-07-22-onto-gate-enforcement-design.md`); this
file records the approach frame.

## D1 — Evidence tokens, not judgment

The binary never judges whether an approach is good; it verifies a recorded
answer exists. Three new gated-core string fields (empty = unanswered):

| Field | Set at | Enforced by | Workflows |
|---|---|---|---|
| `proposal_approved` | open's artifact-review gate | `advance open→design` | full only |
| `approach_confirmed` | design's approach gate | `advance design→build` (with isolation) | full only |
| `close_confirmed` | close's final confirmation gate | `merge-deltas`, `close` | all |

Values are free-form evidence (`YYYY-MM-DD <one-line summary>`); the binary
checks non-empty, mirroring `isolation`'s pattern. Presets skip the two
full-only tokens — their scope gate is the preset skill's own, and blocking
presets on design-phase tokens would contradict their reason to exist.
`close_confirmed` applies everywhere because archiving mutates shared files.

## D2 — Verify cross-check

`advance` out of verify already requires `verify.result == pass`; it
additionally reads `verification.md` and requires a line matching
`^Result: pass` (prefix match, tolerating `(N accepted deviations)`).
Disagreement or an unreadable file refuses with both values named.

## D3 — Derivation ported to Go

`ontostate.DerivePhase(changeDir, st)` implements the dispatcher's evidence
table verbatim (first match wins): archived → done; `Status: Under revision`
→ design; `Result: pass` → close; all tasks checked → verify;
`Status: Confirmed` or preset → build; full with tasks/draft-design → design;
full with proposal only → claimed open/design; else open. `onto status
--json` adds `derived_phase` (and `phase_mismatch: true` when it differs
downward from the claimed phase). The skill keeps the routing *rules* (files
win downward, gates win upward, the open↔design exception) but consumes the
binary's derivation instead of hand-running the table.

## D4 — Preset advance sugar

`onto advance <name> --to build`: loops gated single advances until phase ==
build; refuses when `workflow` is full ("full changes advance one gate at a
time") and when the target is anything but build. Each hop still runs the
full gate set, so this is sugar, not bypass.

## D5 — Compatibility

No schema-version bump: absent fields decode as empty strings. In-flight
full changes refuse their next advance until the skill records the missing
token — intended behavior, called out in the guide.

```

## openspec/changes/onto-gate-enforcement/tasks.md

- Source: openspec/changes/onto-gate-enforcement/tasks.md
- Lines: 1-68
- SHA256: df3a7850f4b1efa0bbadd7a432fd76d3819afc58c4ffb8fba7fdf606142bfdf0

```md
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

```

## openspec/changes/onto-gate-enforcement/specs/onto-evidence-gates/spec.md

- Source: openspec/changes/onto-gate-enforcement/specs/onto-evidence-gates/spec.md
- Lines: 1-94
- SHA256: fcc2fe69d4e67e5ca313d81af6efb81a6c310f5c5c66533bd009a3413ef7a851

[TRUNCATED]

```md
## ADDED Requirements

### Requirement: Judgment gates require recorded evidence tokens
The onto binary SHALL refuse to advance a `workflow: full` change out of
`open` unless `proposal_approved` is non-empty, and out of `design` unless
`approach_confirmed` is non-empty (in addition to the existing isolation
requirement). `onto merge-deltas` and `onto close` SHALL refuse for any
workflow unless `close_confirmed` is non-empty. Each token SHALL be
recordable via `onto set` and SHALL appear in `onto gate --json` while
unanswered. Preset workflows (`fix`, `tweak`) SHALL NOT require
`proposal_approved` or `approach_confirmed`.

#### Scenario: Full change cannot leave design unconfirmed
- **GIVEN** a `workflow: full` change at `phase: design` with isolation set
  but `approach_confirmed` empty
- **WHEN** `onto advance <name>` runs
- **THEN** it refuses, naming `approach_confirmed` and the `onto set` command
  that records it

#### Scenario: Token recorded — advance proceeds
- **GIVEN** the same change after `onto set approach-confirmed <name>
  "2026-07-22 approach B"`
- **WHEN** `onto advance <name>` runs
- **THEN** the phase becomes `build`

#### Scenario: Close refuses unconfirmed global mutation
- **GIVEN** a change at `phase: close` with `close_confirmed` empty
- **WHEN** `onto merge-deltas <name>` or `onto close <name>` runs
- **THEN** each refuses before mutating any shared file, naming the token

#### Scenario: Presets skip the full-only tokens
- **GIVEN** a `workflow: fix` change at `phase: open` with `proposal_approved`
  empty
- **WHEN** `onto advance <name>` runs
- **THEN** it advances (subject to the existing artifact gates)

### Requirement: Verify result is cross-checked against the report
`onto advance` out of `verify` SHALL require both `verify.result == pass` in
state and a line prefixed `Result: pass` in the change's `verification.md`.
When the two disagree, or the file cannot be read, the advance SHALL refuse
naming both values.

#### Scenario: State says pass, report says fail
- **GIVEN** `verify.result: pass` in state and `Result: fail` in
  `verification.md`
- **WHEN** `onto advance <name>` runs
- **THEN** it refuses, naming the state value and the report line

#### Scenario: Accepted deviations still pass
- **GIVEN** `verify.result: pass` and `Result: pass (2 accepted deviations)`
- **WHEN** `onto advance <name>` runs
- **THEN** the phase becomes `close`

### Requirement: The binary derives the working phase from artifacts
The `derived_phase` field of `onto state <change> --json` SHALL be computed
from workspace artifacts by the evidence table (first match wins): archived →
`done`; design `Status: Under revision` → `design`; verification
`Result: pass` → `close`; all tasks checked → `verify`; design
`Status: Confirmed` or preset workspace → `build`; full workflow with an
unconfirmed design draft or incomplete tasks → `design`; full workflow with
only `proposal.md` → the claimed phase when it is `open` or `design`;
`proposal.md` missing → `open`. The output SHALL include
`phase_mismatch: true` when the derived phase differs from the claimed
phase. The field SHALL NOT merely echo the claimed phase.

#### Scenario: Confirmed design derives build
- **GIVEN** a full change whose `design.md` is marked `Status: Confirmed`
  and whose `tasks.md` has unchecked items
- **WHEN** `onto state <name> --json` runs
- **THEN** `derived_phase` is `build`

#### Scenario: Downward mismatch is flagged
- **GIVEN** a change claiming `phase: verify` whose `tasks.md` has unchecked
  items and no confirmed design revision marker
- **WHEN** `onto state <name> --json` runs
- **THEN** `derived_phase` is an earlier phase and `phase_mismatch` is true

### Requirement: Presets advance to build in one gated call
`onto advance <name> --to build` SHALL, for preset workflows only, perform
the gated phase advances from `open` through `build` in one invocation,

```

Full source: openspec/changes/onto-gate-enforcement/specs/onto-evidence-gates/spec.md
