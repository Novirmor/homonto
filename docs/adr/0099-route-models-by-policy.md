# Route models by policy

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

ADR 0016 made `[subagents.<name>.<tool>]` the single source of agent models
and deleted tiers, because tier names coupled config validity to code. ADR
0098 then reserved `[routing]` with fixed selectors (`default`,
`workflows.<w>`, `engines.<e>`, `engines.<e>.stages.<s>`) and fixed
precedence. Two questions remained: what a policy contains, and how a stage
route reaches the harness. OpenCode reads an agent's model from its agent
file at session start. Its task tool takes no per-call model, so homonto has
no runtime hook to switch the model for one stage. Most engine agents also
serve several stages: `onto-explorer` works in open, design, build and
verify.

## Decision

- A policy is harness-native, like a pin: `[routing.<selector>.opencode]`
  with `model` (and `variant`) for `mode = "static"`, the default. Auto
  policies are ADR 0100.
- Routes are resolved at plan and stamped into each rendered agent file,
  the same delivery path pins use. There are no per-stage agent copies and
  no runtime override.
- Engines bind their agents to stages in `[engine.agent_stages]`. A stage
  policy reaches an agent through that binding only. Builtins bind every
  worker.
- Resolution per agent: a pin with a model wins. Otherwise the most specific
  level wins, but only if every place the agent serves agrees on it. An
  agent bound to stages whose policies differ, or shipped by two workflows
  with different policies, gets the next coarser level that agrees. Plan
  warns, and `explain routing` names the places that disagreed.
- The shared coordinator serves every package, so only `[routing.default]`
  reaches it.
- A policy is a model source, so an agent covered by one needs no pin. A pin
  without a model still contributes `steps` and `bash_allow_add`.
- This revises ADR 0016 deliberately. The names in play are still ones the
  config spells out: package names, stage names the engine publishes (a
  contract since ADR 0098) and agent names. No hidden tier exists. Config
  validity never depends on an internal role. An unknown engine, workflow or
  stage in a selector fails at load and names the valid ones.

We rejected per-stage agent variants. They would need every engine skill to
choose the agent name at runtime, and that belongs in the runtime core
(#61). A stage that must run on its own model can get that today by binding
a dedicated agent to it.

## Consequences

One `[routing.default.opencode]` replaces a block per agent. Stage routing
is coarse for multi-stage agents: in onto, a build-only policy reaches
`onto-reviewer` but not `onto-explorer`. That limit is visible in plan output
rather than silent. Renaming a stage or rebinding an agent changes which
policy applies, so both are contract changes for an engine.
