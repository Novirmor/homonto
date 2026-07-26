# Brainstorm Summary

- Change: onto-gate-enforcement
- Date: 2026-07-22

## Confirmed Technical Approach

Evidence-token pattern, mirroring `isolation`: three new gated-core string
fields (`proposal_approved`, `approach_confirmed`, `close_confirmed`; empty =
unanswered, value = free-form evidence). `advance` refuses full-workflow
open-exit without `proposal_approved` and design-exit without
`approach_confirmed`; `merge-deltas` and `close` refuse (any workflow)
without `close_confirmed`. Setters follow the `directive` free-form pattern
(not `enumSetterCmd`). `pendingGates` surfaces all three.

Verify cross-check: `advance` out of verify additionally reads
`verification.md` and requires a `^Result: pass` prefix line; refusal names
both the state value and the file line.

Derivation: `DerivePhase()` today returns the *claimed* phase — the existing
`derived_phase` JSON key in `onto state --json` is untruthful. New
`ontostate.DeriveWorkingPhase(changeDir, st)` implements the dispatcher's
evidence table; `onto state --json` wires `derived_phase` to it and adds
`phase_mismatch`. The dispatcher skill consumes it.

Preset advance: `onto advance <name> --to build`, preset-only, loops gated
single hops, refuses for full and non-build targets.

Confirmed by the maintainer's standing directive ("lets do all of them 1-8",
repeated 2026-07-22) covering items 1, 2, 4, 6 of the critique.

## Key Trade-offs and Risks

- Tokens verify ceremony, not judgment (B1) — a dishonest `onto set` can
  still lie, but an honest agent can no longer *skip*.
- Breaking for in-flight full changes (missing tokens block next advance) —
  intended; the skill asks the gate. No schema-version bump needed (empty
  decode).
- `--to build` must run every per-hop gate or it becomes a bypass — tests
  assert a failing hop surfaces its gate error.

## Testing Strategy

Unit tests per gate (refuse/pass pairs), derivation table row coverage,
cross-check content cases (fail-line, deviations suffix, missing file),
`--to build` happy/refusal paths; full `go test ./...`; onto-lifecycle
Docker E2E extended for the new refusals.

## Spec Patches

`onto-evidence-gates` delta spec corrected: derivation surface is
`onto state <change> --json` (existing key made truthful), not a new
`onto status` flag.
