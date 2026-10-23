# Ground development in CodeGraph

- **Status:** Accepted
- **Date:** 2026-10-23

## Context

AGENTS.md named three local code indexes for developing this repository: OKF
as the default, with CodeGraph and Graphify as optional extras. In practice
OKF's `okf_lookup.py` and `okf_generator.py` were installed nowhere, so the
"default" was always the fallback to direct reading. The other two indexes
answered overlapping questions in different formats. Contributors and agents
had three conventions to learn and no single tool they could rely on.

## Decision

We will use CodeGraph as the only local index for developing homonto. AGENTS.md
and `docs/agents/codegraph.md` describe it. `make index` builds or refreshes
it, and `make affected` uses it to choose tests. OKF and Graphify are no longer
mentioned in the contributor docs, and `docs/agents/okf.md` is removed.

This concerns only how homonto itself is developed. The product's
`code_intel` tooling providers (`okf`, `graphify`, `none`; ADR 0022) are
unchanged, because removing them would break user configurations.

## Consequences

- One tool to install, one convention to follow. Callers, callees, and impact
  queries replace grep for call-graph questions.
- CodeGraph is a third-party CLI. When it is absent, reading files directly
  remains the legitimate fallback, as before.
- The repository no longer develops with the same index it recommends first to
  users. ADR 0022 already made that choice a user's configuration, so the
  divergence carries no product promise.
- `okf_bundle/` and `graphify-out/` stay gitignored so leftover local output
  from before this change can never be committed.
