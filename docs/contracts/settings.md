# Package settings and processes

A package declares the configuration it accepts. Users set it in
`homonto.toml` on the package's own table. homonto validates it at load,
before any plugin runs, and the package reads the resolved values back
through the binary
([ADR 0095](../adr/0095-declare-package-settings-in-the-manifest.md)).

## Declaring a schema (`framework.toml`)

```toml
[settings.depth]
type = "string"                 # string | integer | boolean | list (of strings)
enum = ["quick", "deep"]        # optional; string and integer only
default = "quick"               # optional; must fit type and enum
description = "Review depth"

[settings.limit]
type = "integer"
required = true                 # a required setting has no default

[processes]                     # optional: named definitions the package interprets
description = "Named triage processes"

[processes.fields.steps]
type = "list"
required = true

[processes.fields.strict]
type = "boolean"
default = false
```

Setting and field names match `[a-z][a-z0-9_-]*`. Unknown schema keys fail
(strict decoding). A schema needs `manifest_schema = 2`.

## Setting values (`homonto.toml`)

```toml
[workflows.flow]
source = "local:./flow"
scope  = "project"

[workflows.flow.settings]
limit = 3

[workflows.flow.processes.triage]
steps = ["read", "label"]
```

Settings and processes go on `[workflows.*]`, `[engines.*]` and
`[services.*]` entries only. Load fails with the exact key for:

| Problem | Message |
|---|---|
| unknown key | `workflows.flow.settings.limt is not a known setting (known: depth, limit)` |
| wrong type | `workflows.flow.settings.limit: "three" is not an integer` |
| not in enum | `workflows.flow.settings.depth: "medium" is not one of quick, deep` |
| missing required | `workflows.flow.settings.limit is required (<description>)` |
| package has no schema | `engines.onto.settings: package "onto" declares no settings` |

A process definition is the package's own input, validated against
`[processes.fields]`. It is not an engine stage. Stages stay engine-owned and
are named in the engine contract, so a process cannot add, reorder or
skip one.

## Reading them

```sh
homonto explain settings            # every package, defaults marked
homonto explain settings no --json  # one package, for a skill or script
```

`homonto plan` prints the same `setting:` and `process:` lines before its
changes. The JSON form is the interface for skills: it carries the package,
its config table, each setting's `name`, `type`, `value` and whether it is the
`default`, and the process definitions.

## Builtins

| Package | Setting | Values | Default |
|---|---|---|---|
| `no` | `isolation` | `ask`, `existing`, `worktree` | `ask` |

`no`'s execution reference reads `isolation` before asking how to isolate a
new task.
