# M3 evaluation: auto vs static routing, and decisions per provider

This is the reproducible evaluation for #59. It compares automatic and static
execution-model routing on representative tasks, and evaluates decision
behavior per provider and question type. Everything it needs is in the
repository. It spends inference only when `--paid` is passed.

**Status: method and tooling done; paid results not yet collected.** The
inference budget has not been decided, so no paid run has happened. The
offline evidence below shows the harness works. It says nothing about model
quality. When a paid run is made, add its tables to this page in a new
section with the date and homonto commit.

## What is measured

| Part | Arms or providers | Per run | Grading |
|---|---|---|---|
| Routing | `static` and `auto` arms (`testdata/eval/m3/arms/`) | verification result, wall time, cost (from OpenCode's JSON events, when present), the routed models | the task's `verify` command |
| Decisions | every provider in the eval config | status, failure class, latency, tokens, cost | choice: exact option; yes-no: side of 0.5 plus Brier score; score: inside the labelled range |

Inputs:

- `testdata/eval/m3/tasks/<task>/`: a fixture repository (`repo/`) and
  `task.toml` (`prompt`, `agent`, `verify`, optional `timeout`). The three
  tasks are an off-by-one fix, a rune-aware function to implement, and input
  validation. A test checks that each `verify` fails before any change.
- `testdata/eval/m3/decisions.jsonl`: 12 labelled questions covering
  classification, next-step selection, yes-no and score.
- `testdata/eval/m3/eval-decisions.toml`: the two providers (OpenAI
  Decisions, System One via OpenRouter).

## Running it

Build homonto, check that OpenCode can run the arms' models, and edit the
model ids in `arms/*.toml` and the descriptions in `arms/auto.toml`. Then:

```sh
go build -o /tmp/homonto . && export PATH=/tmp:$PATH

# Dry runs: validate inputs and print the call counts. No network.
go run ./scripts/m3eval decisions --config testdata/eval/m3/eval-decisions.toml
go run ./scripts/m3eval routing --arms static=testdata/eval/m3/arms/static.toml,auto=testdata/eval/m3/arms/auto.toml --reps 3

# Paid runs. --max-calls caps the decision run.
OPENAI_API_KEY=... OPENROUTER_API_KEY=... go run ./scripts/m3eval decisions \
  --config testdata/eval/m3/eval-decisions.toml --paid --out decisions.jsonl
go run ./scripts/m3eval routing --arms static=...,auto=... --reps 3 --paid \
  --work /tmp/m3eval --out routing.jsonl
```

Each routing run copies the fixture, commits it, applies the arm's config,
records `homonto explain routing --json`, runs `opencode run --agent <agent>
--format json --auto <prompt>`, and then runs `verify`. The run directory
keeps the event stream (`.m3eval-events.jsonl`) and the verify log for audit.
Both subcommands append JSON lines to `--out` and print a Markdown summary.

## Offline evidence

`go test ./scripts/m3eval` covers the following:

- the question set is valid and covers every type;
- a dry run makes no provider call;
- grading and the per-provider summary, against fake providers (one
  answering, one rate-limited);
- the `--max-calls` cap;
- task and arm loading;
- the fixtures failing before a fix;
- cost extraction from event lines.

Protocol encoding and decoding are covered by `internal/decision` contract
tests against the documented response shapes.

## Known limits

- Cost for routing runs is read from any OpenCode event object that has both
  `cost` and `tokens`. That event format belongs to OpenCode and has not
  been checked against a paid run. "unknown" in the summary means no event
  carried a cost.
- Auto routing is only as good as the `[routing.models]` descriptions in the
  auto arm (ADR 0100). The comparison measures the policy as configured, not
  automatic selection in general.
- Three tasks are enough to catch a broken arm, but not to rank models. Add
  tasks before drawing conclusions about quality.
