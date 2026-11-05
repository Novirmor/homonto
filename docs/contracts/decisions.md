# Typed decision calls

A package asks a decision model a typed question and gets a validated answer.
Model routing is one use of decisions. Classification, next-step selection
and scoring are others. Decision models are configured apart from the
execution models in [routing.md](routing.md)
([ADR 0101](../adr/0101-deliver-typed-decisions-through-the-binary.md),
[ADR 0102](../adr/0102-classify-and-record-route-and-decision-outcomes.md)).

## Asking

```sh
echo '{"point": "my-workflow.classify", "kind": "choice",
  "question": "Which process fits this issue?",
  "options": ["bug", "feature", "question"],
  "context": {"issue": "Checkout shows a blank screen after paying."}}' | homonto decide
```

The same request from flags, printing only the answer:

```sh
homonto decide --point my-workflow.classify --kind choice \
  --question "Which process fits this issue?" \
  --option bug --option feature --option question \
  --context-file issue=issue.txt --format value
```

| `kind` | Request carries | Result carries |
|---|---|---|
| `choice` | `options` (two or more, distinct) | `choice`, one of the options |
| `yes-no` | nothing extra | `probability` of yes, in [0, 1] |
| `score` | `scale` `{min, max}` and `levels` (2–10 names, lowest first) | `score` within the scale |

`point` is `<package>.<point>`, naming the package that asks and its
decision point. `context` is the evidence, as named strings. Results name
their `provider` and may carry a `confidence` in [0, 1]. A result with the
wrong kind, an extra field or an out-of-range value never reaches the
caller.

Flags: `--request FILE`, `--task NAME` (kept in the record), `--no-record`,
`--format json|value`, and for flag-built requests `--point`, `--kind`,
`--question`, `--option`, `--level`, `--min`, `--max`, `--context KEY=VALUE`
and `--context-file KEY=PATH`. The JSON outcome and the `value` answer go to
stdout. With `--format value`, a failure's message goes to stderr.

## Outcomes

| Status | Exit | Meaning |
|---|---|---|
| `answered` | 0 | a validated result |
| `uncertain` | 10 | a result whose certainty is below `min_confidence` (the result is still printed) |
| `failed`, class `refused` | 11 | the model declined |
| `failed`, any other class | 12 | `not-configured`, `unsupported`, `unavailable`, `timeout`, `rate-limited`, `auth`, `rejected` or `invalid` |

Certainty is the provider's confidence. A yes-no answer without one uses
`max(p, 1-p)`. An answer with neither counts as zero. homonto never retries
and never invents an answer. On `uncertain` or a failure the plugin applies
its own safe default: the example workflow asks the user.

Each outcome is appended to `.homonto/decisions.jsonl` without the question
or context. `homonto status` shows the latest five.

## Configuration

```toml
[decisions]
default = "openai"          # provider for unlisted points (optional)
min_confidence = 0.6        # optional, every point
timeout = "30s"             # optional, per call

[decisions.providers.openai]
protocol = "openai-decisions"          # POST https://api.openai.com/v1/decisions by default
model = "gpt-6-luna"
api_key_env = "OPENAI_API_KEY"

[decisions.providers.jev]
protocol = "systemone"                 # Jev / System One-compatible
endpoint = "https://openrouter.ai/api/v1/systemone"   # required: compatible services differ
model = "typesafe/jev-1.14"
api_key_env = "OPENROUTER_API_KEY"

[decisions.points."my-workflow.classify"]
provider = "jev"
min_confidence = 0.8
```

- A point listed under `points` must belong to a declared package.
- Endpoints are https. Plain http is allowed only to localhost.
- Credentials come only from `api_key_env`. They are sent as a bearer token
  and redacted from every error and record.

## Protocols

| `protocol` | Request | yes-no | choice | score |
|---|---|---|---|---|
| `systemone` | `model`, `state` (the context), `questions` map | `noul` | `choice` with option `criteria` | `score` with level `criteria` |
| `openai-decisions` | `model`, `input` (the context as text), `questions` array | `predicate` | `choice` with `choices` | `score` with `levels` |

Model-dependent System One extensions (box questions, image state) are not
part of this API. An OpenAI `refusal` answer becomes the `refused` failure.
Adding a protocol means registering a Go type in `internal/decision`. It is
a homonto change, not a package.

A live check against a real provider is opt-in, because it costs money:

```sh
HOMONTO_LIVE_DECISIONS=openai-decisions HOMONTO_LIVE_DECISIONS_KEY=... \
HOMONTO_LIVE_DECISIONS_MODEL=gpt-6-luna go test ./internal/decision -run Live -v
```
