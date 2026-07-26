# Brainstorm Summary

- Change: optional-tooling-providers
- Date: 2026-07-26

## Confirmed Technical Approach

A `[tooling]` config table declares two providers from closed sets
(`shell_proxy`: rtk/none; `code_intel`: graphify/okf/none), both defaulting to
`none`. Provider prose lives in `catalog/tooling/*.md` fragments. At
materialize time the renderer concatenates a generated header plus the two
selected fragments into `references/tooling.md` inside each framework's
dispatcher skill; shipped `SKILL.md` files stay byte-stable and defer to it.

Resolved during brainstorming:

- **Fragment digest belongs in `toolingFP`, not `ContentFingerprint`.** The
  latter digests per-named-resource and fragments are not a declared resource.
  `toolingFP = sha256(shellProxy, codeIntel, selected fragment bytes)`, so an
  edit to an unselected fragment causes no churn.
- **Dispatcher = the skill named after its framework** (user choice). Already
  true for onto and to, and documented in `framework.toml`'s own comment. No
  new schema key.
- **`doctor` reports a declared-but-absent provider** as a warning finding
  (user choice), probing with `exec.LookPath` only — no execution, so the
  v0.7.0 exec-timeout concerns do not apply.
- **`State.SubagentRenderFingerprint` is renamed to `RenderFingerprint` now,
  with a schema bump 1 to 2** (user choice, against the initial
  recommendation). The migration turned out to be free: the field is a cache
  whose documented semantics are already "absent = force re-render", and this
  change bumps the catalog version, which forces re-materialization for every
  user anyway. No value-preserving shadow field is needed.
- `to` renders the same sidecar for symmetry; fragment text stays
  framework-neutral.
- Unknown keys inside `[tooling]` are rejected by the existing strict-decode
  path rather than bespoke validation.

## Key Trade-offs and Risks

- **Highest risk: silently dropping instruction while neutralizing 11 files.**
  Mitigation: fragments start as verbatim moves of the existing prose,
  reviewed as a pure move; wording changes only in a later task.
- Defaulting both providers to `none` changes shipped skill behavior for
  existing users. Mitigation: release note plus the restore snippet.
- A hand-deleted sidecar would otherwise stay missing under an up-to-date
  fingerprint. Mitigation: the presence check becomes dispatcher-aware.
- The neutrality test could over-match legitimate provider mentions in docs.
  Mitigation: scope it to `catalog/`, excluding `catalog/tooling/`.

## Testing Strategy

Table-driven config tests (absent, partial, full, invalid value, unknown key);
golden files for all six provider combinations; gate tests for re-render on
config change, no-op on unchanged config, and repair after sidecar deletion; a
neutrality test asserting no provider name appears in `catalog/` outside
`catalog/tooling/`; doctor finding tests; and the onto and to Docker lifecycle
suites extended to assert the sidecar matches the declared providers.

## Spec Patches

Two supplementary scenarios, no scope or structure change:

1. A doctor scenario: a declared-but-absent provider produces a warning-level
   finding and never blocks apply.
2. A migration scenario: a schema-version-1 state file loads and forces one
   re-render rather than failing.
