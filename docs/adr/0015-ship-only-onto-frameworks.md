# Ship only homonto-native frameworks (onto, and later `to`)

- **Status:** Superseded by 0023
- **Date:** 2026-07-15

## Context

The bundled catalog shipped four frameworks: `onto` (homonto's native,
binary-enforced workflow) and three vendored third-party workflow stacks —
`comet`, `openspec`, and `superpowers` — that predate onto and exercise no
binary gate. Vendoring them meant homonto carried, versioned, and implicitly
endorsed ~30 skills it does not maintain, and every catalog mechanism
(dependencies, capabilities, ranged constraints) existed in production only to
serve their interdependencies. Meanwhile the catalog had grown a second,
deliberate distribution channel: **loose** framework-agnostic skills and
commands (`handoff`, `grilling`), indexed individually when unclaimed by any
framework.

## Decision

The catalog ships **only homonto-native frameworks**: `onto` today, and a
planned second framework named `to`. Loose skills/commands remain a separate,
supported channel. `comet`, `openspec`, and `superpowers` are removed —
frameworks homonto does not author are out of scope for the bundled catalog
(users who want other content have `local:` and pinned `remote:` sources).

This does not change how this repository is developed: the maintainers still
use Comet day-to-day, from their own setup, not from the catalog (see
`docs/personas.md` and `docs/guides/comet-workflow.md`; dogfooding onto is
deferred to v1 by ADR 0012's successor decision).

## Consequences

- **Breaking:** a config declaring `[frameworks.comet|openspec|superpowers]`
  (or any of their skills/subagents as `builtin:`, e.g. `comet-navigator`)
  fails at expand with `catalog: unknown framework` / `unknown subagent`.
  There is no migration path; the last release carrying them is v0.2.2.
- Framework dependency/capability/range mechanics stay in the engine and keep
  fixture-based tests (`internal/catalog`), but no shipped framework exercises
  them; `TestNew_CatalogShipsOnlyOnto` pins the shipped surface.
- The `homonto-expanded` E2E suite's standalone-subagent path now exercises a
  `local:` source instead of a second builtin framework — incidentally
  widening real coverage to local agents.
