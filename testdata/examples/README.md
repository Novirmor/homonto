# Example packages

Minimal external packages, one per kind, that the conformance check and the
contract tests consume exactly as a user's package would be consumed. They
load as `local:` roots and, packed and digest-pinned, as `remote:` sources.

| Package | Kind | Shows |
|---|---|---|
| [`example-engine`](example-engine/framework.toml) | engine | stateless lifecycle, entry skill, named stages, an intent-rendered worker, a setting |
| [`example-workflow`](example-workflow/framework.toml) | workflow | the shared coordinator, an entry command, a required service |
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
