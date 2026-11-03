# Model routing and decision calls (reserved)

M2 reserves the surfaces M3 fills (#32). Nothing here is implemented yet:
`[routing]` and `[decisions]` fail at load with a pointer to this page, and
every decision call returns "no decision provider is configured"
([ADR 0098](../adr/0098-reserve-routing-and-decision-surfaces.md)). The
shapes below are fixed, so M3 adds behavior without changing the M2
contracts.

## Addressable stages

Every engine names its stages in `[engine].stages` ([engine.md](engine.md)).
Stage identifiers are part of the engine's contract: renaming one is a
breaking change for any routing policy that targets it.

| Engine | Stages |
|---|---|
| `onto` | open, design, build, verify, close |
| `to` | plan, do, done |
| `no` | implement, review |

## Routing policies

A routing policy chooses the execution model for work. It is selected by
where the work runs, never by harness. The policy's own fields (static
model lists, automatic selection, provider settings) are defined in M3 and
stay harness-native, as ADR 0082 requires.

Reserved selectors, from least to most specific:

| Table | Applies to |
|---|---|
| `[routing.default]` | everything |
| `[routing.workflows.<workflow>]` | work a workflow starts |
| `[routing.engines.<engine>]` | every stage of an engine |
| `[routing.engines.<engine>.stages.<stage>]` | one declared stage |

Precedence: the most specific selector wins. An explicit
`[subagents.<name>.<tool>]` block (ADR 0016) always wins over every policy,
because it is the user's own statement about one agent. A policy only fills
models nobody pinned. Until M3, every rendered agent still needs its explicit
block.

## Decision calls

A package asks a decision model a typed question and gets a validated answer
(`internal/decision`). This is the shape a skill or binary will use through
homonto. The transport (CLI or MCP tool) is defined with the first provider.

Request:

```json
{
  "point": "onto.next-step",
  "kind": "choice",
  "question": "Repair or re-verify?",
  "options": ["repair", "verify"],
  "context": {"findings": "2 major"}
}
```

| `kind` | Request carries | Result carries |
|---|---|---|
| `choice` | `options` (two or more, distinct) | `choice`, one of the options |
| `yes-no` | nothing extra | `probability` of yes, in [0, 1] |
| `score` | `scale` `{min, max}`, min < max | `score` within the scale |

Every result names its `provider` and may carry a `confidence` in [0, 1]. A
result with the wrong kind, an extra field, or an out-of-range value is
rejected before it reaches the caller.

`point` is `<package>.<point>`: the package that asks, and a name for the
decision point, so routing and audit can address it. Model routing is one
consumer of decisions. Classification, next-step selection and scoring are
others. Provider protocols (Jev/System One-compatible endpoints, native
OpenAI Decisions) and `[decisions]` configuration are #55–#58.
