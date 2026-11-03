# Select models automatically at plan

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

M3 asks for automatic selection of execution models from what the harness
can run, under constraints such as cost, latency and allowed providers, with
a static mode and a deterministic fallback. OpenCode 2.2's `opencode models`
prints `provider/model` ids, one per line, with no cost, latency or
capability data. Agent files carry a static model (ADR 0099), so selection
has to finish before apply writes them.

## Decision

- `mode = "auto"` policies select at plan, once per command invocation, from
  `opencode models` run in the project root. A config with only static
  policies never runs discovery.
- Constraints: `candidates` (an ordered shortlist), `providers` (id
  prefixes), `needs` (tags), `max_cost`, `max_latency`. Ordering: `prefer =
  "listed" | "cost" | "latency"`, then candidate order, then id.
- Model properties come only from `[routing.models.opencode."<id>"]` (`cost`,
  `latency`, `tags`), in the operator's own units. A constraint on a property
  the config does not state excludes that model. Nothing is guessed from a
  model name.
- `fallback` is required. Homonto uses it when discovery fails or reports
  nothing, and when no model qualifies. If discovery succeeded and the
  fallback is not among the reported models, plan fails: the harness cannot
  run that model.
- Every selection carries its reason. A fallback is a plan warning.

We rejected hard-coding model metadata or fetching a provider catalog. A
built-in price table goes stale silently, and a network fetch at plan would
make plan nondeterministic and dependent on a third party.

## Consequences

Auto routing is only as good as the metadata the operator writes. Without
`[routing.models]`, the only usable constraints are `providers` and
`candidates`. Discovery adds one `opencode models` call to plan when an auto
policy exists. A model that becomes available or disappears changes the
selection at the next plan, which re-renders the agent. That is intended:
the record says why (ADR 0102).
