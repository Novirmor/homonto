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
