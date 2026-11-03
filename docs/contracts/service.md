# Service contract

A service package connects one host or ticket service. It declares what the
service can do, how homonto reaches it, what credentials it needs, and which
shell rules it adds to rendered agents. It implies no engine and no harness
feature ([ADR 0094](../adr/0094-package-host-services-separately.md)).

```toml
manifest_schema = 2
name = "tracker"
kind = "service"
version = "0.1.0"

[service]
id = "tracker"                       # the service id workflows require
# runtime = "github"                 # optional: enable a provider built into homonto
operations = ["read", "publish"]     # read | draft | publish
transport = "tracker-cli"            # the CLI or command the service operates through
credentials = [
  { kind = "env", name = "TRACKER_TOKEN" },          # an environment variable, never its value
  { kind = "cli", check = "tracker-cli auth status" }, # a read-only login check
]

[permissions.coordinator]
bash_ask  = ["tracker-cli issue delete*"]
bash_deny = ["tracker-cli admin*"]

[permissions.workers]
bash_deny = ["tracker-cli *"]
```

| Field | Rule |
|---|---|
| `id` | Identifier. One installed package per id. |
| `runtime` | Empty, or a draft/publication provider built into the binary: `github`, `gitlab`, `bitbucket`. A package cannot add a provider; providers are Go code (ADR 0090). |
| `operations` | At least one of `read`, `draft`, `publish`. `draft` needs `runtime`, because drafts live in the binary's runtime. |
| `transport` | Required. |
| `credentials` | `cli` entries need `check`, `env` entries need a variable `name`. |
| `[permissions]` | Shell patterns, single-line. |

A service ships no subagents and no plugins, and cannot require another
service. It may ship skills and commands.

## Permissions only tighten

Rendered agents take a service's rules after their own:

- the coordinator (every primary agent) gains `bash_ask` and `bash_deny`;
- every other agent with a shell gains `[permissions.workers].bash_deny`;
- agents with no shell are unchanged.

A service can never add an allow. Installing one can only make prompts or
refusals more likely, so a `local:` or digest-pinned `remote:` service adds
no new authority.

## Depending on a service

A workflow or engine names each service it needs and a default package:

```toml
[requires.services]
github = "host-gh"
```

At load, homonto installs the default beside the requiring package (same
scope and targets) unless the config declares a service with that id under
`[services.*]`. So a custom workflow can use GitHub drafts without installing
`h-workflow-gh`, and a user can swap in their own GitHub service.

## Builtins

| Package | Service | Runtime | Transport | Credentials |
|---|---|---|---|---|
| `host-gh` | `github` | `github` | `gh` | `gh auth status` |
| `host-gl` | `gitlab` | `gitlab` | `glab` | `glab auth status` |
| `host-bb` | `bitbucket` | `bitbucket` | `homonto host bitbucket api` | `HOMONTO_BITBUCKET_TOKEN`, `homonto host bitbucket auth status` |

The builtin services contribute no `[permissions]`. The coordinator and
implementer baselines already carry the host-CLI rules, because an engine
delivers through `gh`, `glab` or the Bitbucket transport even when no service
is installed. This is the one builtin exception, recorded in ADR 0094.
