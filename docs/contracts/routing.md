# Model routing and decision calls

A routing policy chooses the execution model an agent runs on. It is
addressed by where the work runs, never by harness, and its fields are
harness-native ([ADR 0098](../adr/0098-reserve-routing-and-decision-surfaces.md),
[ADR 0099](../adr/0099-route-models-by-policy.md)). OpenCode is the only
harness with fields today.

## Addressable stages

Every engine names its stages in `[engine].stages` and binds its agents to
them in `[engine.agent_stages]` ([engine.md](engine.md)). Renaming a stage or
rebinding an agent is a breaking change for any policy that targets it.

| Engine | Stages |
|---|---|
| `onto` | open, design, build, verify, close |
| `to` | plan, do, done |
| `no` | implement, review |

| Engine | Agent | Bound stages |
|---|---|---|
| `onto` | `onto-explorer` | open, design, build, verify |
| `onto` | `onto-implementer` | build, verify |
| `onto` | `onto-reviewer` | build |
| `onto` | `onto-skeptic` | verify |
| `to` | `to-explorer` | plan, do |
| `to` | `to-implementer` | do, done |
| `to` | `to-reviewer` | do |
| `to` | `to-skeptic` | done |
| `no` | `no-implementer` | implement |
| `no` | `no-reviewer` | review |

## Selectors and precedence

| Table | Applies to |
|---|---|
| `[routing.default.opencode]` | every agent, including the shared `homonto` coordinator |
| `[routing.workflows.<workflow>.opencode]` | agents the workflow ships |
| `[routing.engines.<engine>.opencode]` | agents the engine ships |
| `[routing.engines.<engine>.stages.<stage>.opencode]` | agents bound to that stage |

1. An explicit `[subagents.<name>.opencode]` `model` wins over every policy
   (ADR 0016). A pin without a `model` still contributes `steps` and
   `bash_allow_add`.
2. Otherwise the most specific selector wins, **if every place the agent
   serves agrees**. `onto-implementer` serves build and verify. A build
   policy alone does not reach it, because verify would still use the
   engine policy. Set the same policy on both stages, or pin the agent.
   When places disagree, the next coarser level that agrees applies, and
   `plan` warns.
3. The shared coordinator takes only `[routing.default]`.

An agent covered by a policy needs no pin. An agent with neither fails at
load: `subagents.<name>.opencode model is required`.

## Static policies

```toml
[routing.default.opencode]
model = "anthropic/claude-sonnet-5"

[routing.engines.onto.stages.verify.opencode]
model = "openai/gpt-6"
variant = "high"
```

## Auto policies

`mode = "auto"` selects at plan from the models `opencode models` reports
([ADR 0100](../adr/0100-select-models-automatically-at-plan.md)):

```toml
[routing.engines.no.opencode]
mode = "auto"
providers = ["anthropic", "openai"]   # optional: allowed provider prefixes
candidates = ["openai/gpt-6-mini", "anthropic/claude-haiku-5"]  # optional ordered shortlist
needs = ["tools"]                      # optional: tags each model must carry
max_cost = 5.0                         # optional: needs a described cost
max_latency = 2.0                      # optional: needs a described latency
prefer = "cost"                        # listed (default) | cost | latency
fallback = "anthropic/claude-sonnet-5" # required

[routing.models.opencode."openai/gpt-6-mini"]
cost = 0.6          # your own unit, compared only
latency = 0.8
tags = ["tools"]
```

- Filters: available (and in `candidates` when set), then `providers`,
  `needs`, `max_cost` and `max_latency`. A constraint on a property the config
  does not describe excludes the model.
- Order: `prefer`, then candidate order, then id.
- Fallback: used when discovery fails or reports nothing, or when nothing
  qualifies. If discovery succeeded and the fallback is not reported, plan
  fails.
- Discovery runs once per command, and only when an auto policy applies.

## Seeing routes

- `homonto explain routing [agent] [--json]` shows each agent's model, the
  selector that chose it, the reason, and any disagreement.
- `plan` warns about disagreements and fallbacks.
- `apply` records the routes it stamped into `.homonto/routing.json`.
  `homonto status` prints the policy-routed agents from that record.

## Decision calls

A package asks a decision model a typed question and gets a validated answer
(`internal/decision`). Decision providers and the `homonto decide` command
are described in [decisions.md](decisions.md).
