# Classify and record route and decision outcomes

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

Routes and decisions can fail: discovery can be unavailable, a model can be
missing, a provider can time out, refuse or answer with low confidence. M3
requires defined behavior for each case and a record of the selected route
and returned signals where workflows keep evidence. Decisions can happen
outside any workspace task, for example in a workflow's triage before an
engine starts.

## Decision

- **Routes.** Static policies never fail at plan. An auto policy falls back
  to its declared model when discovery fails or nothing qualifies, and plan
  warns. If discovery succeeded and the fallback is not available, plan
  fails. A policy that an agent's stages or owners disagree on is skipped
  with a plan warning. Apply writes the stamped routes, their reasons and
  the discovery state to `.homonto/routing.json`, and removes the file when
  no policy applies. `status` prints policy-routed agents.
- **Decisions.** Every call ends `answered`, `uncertain` or `failed`. A
  failure has one class: `not-configured`, `unsupported`, `unavailable`,
  `timeout`, `rate-limited`, `auth`, `rejected`, `invalid` or `refused`.
  `uncertain` keeps the result: the certainty is below the point's
  `min_confidence`. Certainty is the provider's confidence, or `max(p, 1-p)`
  for yes-no, or zero when neither exists. Exit codes: 0, 10 uncertain,
  11 refused, 12 any other failure.
- Homonto never retries a call and never substitutes an answer. The plugin
  knows its safe default. The example workflow asks the user.
- Each outcome is appended to `.homonto/decisions.jsonl` next to the
  routing record, with the point, kind, status, result, failure class,
  provider, protocol, served model, latency, usage and an optional
  `--task`. The question and its context are not recorded: they are the
  plugin's evidence and may be sensitive. `status` shows the latest five
  calls, and `status --output json` shows twenty.

We rejected recording decisions only in workspace-task metadata, because
calls made outside a task would then leave no trace.

## Consequences

A workflow can branch on an exit code without parsing JSON. Audit sees what
was decided and by whom, but not why, because the evidence stays with the
plugin. The decision log grows without bound. Rotation is left to the
operator until it matters.
