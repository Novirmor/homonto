# Example packages

Minimal external packages, one per kind, that the conformance check and the
contract tests consume exactly as a user's package would be consumed. They
load as `local:` roots and, packed and digest-pinned, as `remote:` sources.

| Package | Kind | Shows |
|---|---|---|
| [`example-engine`](example-engine/framework.toml) | engine | stateless lifecycle, entry skill, named stages, an intent-rendered worker, a setting |
| [`example-workflow`](example-workflow/framework.toml) | workflow | the shared coordinator, an entry command, a required service, a typed decision (`example-workflow.classify`) that chooses which configured process runs |
| [`example-service`](example-service/framework.toml) | service | operations, transport, credentials, tighten-only shell rules |

```sh
homonto package check testdata/examples/example-engine testdata/examples/example-workflow testdata/examples/example-service
```

The workflow requires the `tracker` service from `example-service`, so the
two are checked together. A config installs them like this:

```toml
[engines.example-engine]
source = "local:./testdata/examples/example-engine"
scope  = "project"

[workflows.example-workflow]
source = "local:./testdata/examples/example-workflow"
scope  = "project"

[services.example-service]
source = "local:./testdata/examples/example-service"
scope  = "project"
```

The workflow's `classify.sh` asks `homonto decide` which triage process fits
an issue, and prints `ask` when the decision is uncertain or fails. Route
its point to a provider ([decisions.md](../../docs/contracts/decisions.md)):

```toml
[workflows.example-workflow.processes.bug]
steps = ["reproduce", "label bug", "hand to engine"]

[decisions.providers.openai]
protocol = "openai-decisions"
model = "gpt-6-luna"
api_key_env = "OPENAI_API_KEY"

[decisions.points."example-workflow.classify"]
provider = "openai"
```
