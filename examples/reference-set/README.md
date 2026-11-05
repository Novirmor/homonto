# Reference extension set

A complete external engine, workflow and service, built only on homonto's
public package contract ([docs/contracts/](../../docs/contracts/)). homonto's
own source never names them. They are the backing code for the
[authoring guide](../../docs/guides/ticket-workflow.md) and the reference
entry of the real-harness matrix, and they run unchanged on OpenCode and
Claude Code.

They are published separately at
[Novirmor/homonto-reference-set](https://github.com/Novirmor/homonto-reference-set),
whose releases carry the pinned archives and whose CI drives them against a
pinned homonto commit. This copy is the offline fixture for homonto's own tests;
change both together.

| Package | Kind | Shows |
|---|---|---|
| [`ticket-service`](ticket-service/framework.toml) | service | a company ticket tracker behind `ticket-cli` (a file-backed stand-in for Jira or Linear), credentials, tighten-only shell rules |
| [`ticket-engine`](ticket-engine/framework.toml) | engine | a **native** lifecycle owned by its own binary (`bin/ticket-engine`), named stages bound to agents, an intent-rendered implementer and reviewer, a setting |
| [`ticket-workflow`](ticket-workflow/framework.toml) | workflow | ticket intake → engine → review → delivery, processes per ticket type, a required service |

## Install

Publish each directory as a `.tar.gz` with `framework.toml` at its root (or
use them in place with `local:`), put `ticket-engine/bin` and
`ticket-service/bin` on `PATH`, and pin each archive:

```toml
schema_version = 2
harnesses = ["opencode", "claude"]

[repos]
api = "../api"

[engines.ticket-engine]
source = "remote:https://example.com/ticket-engine-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[workflows.ticket-workflow]
source = "remote:https://example.com/ticket-workflow-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[services.ticket-service]
source = "remote:https://example.com/ticket-service-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[workflows.ticket-workflow.processes.bug]
steps = ["reproduce", "fix", "review"]

[routing.default.opencode]
model = "anthropic/claude-sonnet-5-5"

[routing.default.claude]
model = "sonnet"
```

```sh
homonto package check examples/reference-set/ticket-engine examples/reference-set/ticket-workflow examples/reference-set/ticket-service
```

`internal/engine/referenceset_test.go` consumes the three as digest-pinned
`remote:` archives, projects them onto both harnesses, and drives the engine
binary through a workspace task's start, resume, integration check and
release.
