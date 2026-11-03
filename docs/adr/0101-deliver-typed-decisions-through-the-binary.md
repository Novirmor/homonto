# Deliver typed decisions through the binary

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

ADR 0098 fixed the plugin-facing decision shape (`choice`, `yes-no`,
`score`, addressed by `<package>.<point>`) and left the transport, the
configuration and the protocols open. Engines and workflows are skills that
run on any harness. The control plane is the binary that skills shell out
to. The two protocols differ: System One-compatible endpoints take a `state`
and a map of named `noul`/`choice`/`score` questions with `criteria`.
OpenAI Decisions takes `input` and an array of `predicate`/`choice`/`score`
questions with `choices`/`levels`, and can return a per-question
`refusal`. Neither scores on a numeric range: both score on 2–10 named
levels.

## Decision

- Plugins call `homonto decide`: a JSON request on stdin or `--request`, or
  the same request built from flags for shell scripts. It prints the
  outcome as JSON, or the bare answer with `--format value`. The exit code
  carries the status. We rejected an MCP tool for now. It would tie the
  capability to harnesses with MCP, and the runtime core (#61) can wrap the
  same service later.
- `[decisions]` is separate from routing: `providers.<name>` (`protocol`,
  `endpoint`, `model`, `api_key_env`), a `default` provider, per-point
  `points."<pkg>.<point>"` routing, `min_confidence` and `timeout`. A
  decision model is never an execution model.
- Credentials come only from the named environment variable. Endpoints must
  be https (plain http only to loopback), and a key is redacted from every
  error.
- Protocol differences stay in `internal/decision` protocol types. Each one
  encodes one request, decodes one answer and rejects what it cannot
  express before any network call. `yes-no` maps to `noul`/`predicate`. A
  score request carries `levels`, and the answer's level position maps
  linearly onto its `scale`. Model-dependent extensions (System One box
  questions, images) are not in the shared API, so no request can reach a
  model that lacks them.
- Extension point: a protocol is a Go type registered under a name
  (`decision.RegisterProtocol`). A new protocol is a homonto change, not a
  plugin. Arbitrary code would otherwise handle provider credentials.

## Consequences

Any skill on any harness can ask a typed question with one shell command,
and swapping providers is a config change. The decoders follow the
documented shapes. They were not checked against paid calls, so the opt-in
live smoke test (`HOMONTO_LIVE_DECISIONS`) is the evidence before relying
on a provider. Each call sends one question, which forgoes both protocols'
batching. Batching can be added without changing the request shape.
