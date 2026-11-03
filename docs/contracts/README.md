# Package contracts

New to writing a package? Start with [authoring.md](authoring.md) and the
[example packages](../../testdata/examples/README.md), and check yours with
`homonto package check`.

A homonto package is a directory with a `framework.toml` manifest and the
skills, commands, subagents and plugins it lists. homonto installs it from the
bundled catalog (`builtin:<name>`), a local directory (`local:<path>`) or a
digest-pinned remote (`remote:<url>`).

`manifest_schema = 2` is the public contract ([ADR 0092](../adr/0092-publish-the-package-contract.md)).
The kinds are:

| Kind | Owns | Config table | Contract |
|---|---|---|---|
| `engine` | implementation stages, transitions, repair loop | `[engines.<name>]` | [engine.md](engine.md) |
| `workflow` | the external process: intake, engine invocation, review, delivery | `[workflows.<name>]` | [workflow.md](workflow.md) |
| `service` | a reusable host or ticket service: operations, transport, credentials, shell rules | `[services.<name>]` | [service.md](service.md) |

Harness requirements: [capabilities.md](capabilities.md). Package settings and
process definitions: [settings.md](settings.md). Model routing:
[routing.md](routing.md). Typed decision calls: [decisions.md](decisions.md).

Every kind shares these manifest fields:

```toml
manifest_schema = 2          # the contract version; newer fails closed
name = "my-engine"           # equals the directory (catalog) or config key (local/remote)
kind = "engine"              # engine | workflow | service
version = "0.1.0"            # required
description = "..."

[compat]
homonto = ">=0.37.0"         # optional homonto version range

[requires]
harness = ["skills", "subagents"]   # harness capabilities (capabilities.md)

[requires.services]
github = "host-gh"                  # service id = default service package (service.md)

[dependencies]
frameworks = ["other@>=0.2.0"]      # packages installed with this one
capabilities = ["review@1"]         # package interfaces another package provides

[provides]
capabilities = ["review@1"]

[skills]       # name = "path/to/skill-dir"
[commands]     # name = "path/to/command.md"
[subagents]    # name = "path/to/agent.md"
[plugins]      # name = "path/to/plugin-dir" (holds index.ts)

[settings.<name>]   # configuration the package accepts (settings.md)
[processes]         # named definitions the package interprets (settings.md)
```

Schema 2 manifests decode strictly: an unknown key is an error that names it.
Resource names are plain single path components. Two packages may share a
resource name only with byte-identical content.

The shared coordinator (agent `homonto`, skill `homonto`, plugin
`homonto-workflow`) is never declared by a package. An engine or workflow
that runs under it sets `coordinator = "shared"` in its kind table.

Schema 0/1 manifests still load as resource bundles, but they carry no
contract: they cannot be selected as an engine in a workspace task, declare
settings, or pass `homonto package check`.
