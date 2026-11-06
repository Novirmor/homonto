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
pinned homonto commit. [`published.toml`](published.toml) names the release
homonto tests against (`v0.1.0`, commit `37931ba`) and each package's URL and
content digest. The E2E `reference` cell installs that release over https.
This copy is the offline fixture for homonto's Go tests, and
`TestReferenceSetMatchesItsPublishedPins` fails when it no longer pins to the
digests in `published.toml`. To change a package, change it here and in the
published repository, cut a new tag there, and move `published.toml` to it in
the same commit. `task reference-published` checks the live release assets.

| Package | Kind | Shows |
|---|---|---|
| [`ticket-service`](ticket-service/framework.toml) | service | a company ticket tracker behind `ticket-cli` (a file-backed stand-in for Jira or Linear), credentials, tighten-only shell rules |
| [`ticket-engine`](ticket-engine/framework.toml) | engine | a **native** lifecycle owned by its own binary (`bin/ticket-engine`), named stages bound to agents, an intent-rendered implementer and reviewer, a setting |
| [`ticket-workflow`](ticket-workflow/framework.toml) | workflow | ticket intake → engine → review → delivery, processes per ticket type, a required service |

## Install

Install the published `v0.1.0` archives (or use these directories in place
with `local:`), put `ticket-engine/bin` and `ticket-service/bin` on `PATH`,
and pin each archive:

```toml
schema_version = 2
harnesses = ["opencode", "claude"]

[repos]
api = "../api"

[engines.ticket-engine]
source = "remote:https://github.com/Novirmor/homonto-reference-set/releases/download/v0.1.0/ticket-engine-0.1.0.tar.gz"
digest = "sha256:7ea9996c1f35e01d0e60174daa4bf548c8e42f0609be79ea6518079ad7126143"
scope  = "project"

[workflows.ticket-workflow]
source = "remote:https://github.com/Novirmor/homonto-reference-set/releases/download/v0.1.0/ticket-workflow-0.1.0.tar.gz"
digest = "sha256:1a20b9c7506926203500f1fe5c326c3a14439e5f5eaab07dfd6aeaaee2525b80"
scope  = "project"

[services.ticket-service]
source = "remote:https://github.com/Novirmor/homonto-reference-set/releases/download/v0.1.0/ticket-service-0.1.0.tar.gz"
digest = "sha256:3738da490229a98a29e72dde2fb6bf896026e1bb0a39648b32cf08a4dac3fc8f"
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
