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
