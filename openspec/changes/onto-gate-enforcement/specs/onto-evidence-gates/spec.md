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
running every per-hop gate. It SHALL refuse for `workflow: full` and for any
`--to` target other than `build`.

#### Scenario: Fix preset reaches build in one call
- **GIVEN** a `workflow: fix` change at `phase: open` with `proposal.md`,
  `tasks.md`, and isolation set
- **WHEN** `onto advance <name> --to build` runs
- **THEN** the phase is `build`

#### Scenario: Full workflow refused
- **GIVEN** a `workflow: full` change at any phase
- **WHEN** `onto advance <name> --to build` runs
- **THEN** it refuses, telling the caller full changes advance one gate at a
  time
