# Writing a package

This walks through building an engine, a workflow or a service, installing
it from a directory or a pinned archive, and checking it. The rules are in
the per-kind contracts. Working examples of each kind are in
[`testdata/examples/`](../../testdata/examples/README.md).

## 1. Lay out the package

```text
my-engine/
  framework.toml
  skills/my-engine/SKILL.md
  commands/my-engine.md
  subagents/my-reviewer.md
```

Paths in `framework.toml` are relative to the package root. Every name is a
plain path component and must not collide with a shipped resource of
different content.

## 2. Write the manifest

Start from the example for your kind and keep `manifest_schema = 2`:

- engine: [`example-engine`](../../testdata/examples/example-engine/framework.toml), rules in [engine.md](engine.md);
- workflow: [`example-workflow`](../../testdata/examples/example-workflow/framework.toml), rules in [workflow.md](workflow.md);
- service: [`example-service`](../../testdata/examples/example-service/framework.toml), rules in [service.md](service.md).

List the harness capabilities the package needs in `[requires].harness`
([capabilities.md](capabilities.md)), and any configuration it takes in
`[settings.*]` / `[processes]` ([settings.md](settings.md)).

## 3. Write the agents

Give every agent a `homonto:` intent block (read-only, shell rules, network,
dialogs, spawn list) instead of a hand-written `permission:` map. homonto
renders it per harness, and the user picks the model in
`[subagents.<name>.opencode]` ([subagents guide](../guides/subagents.md)). Do
not put `model`, `variant`, `steps` or `permission` in the frontmatter.
Skills need frontmatter with `name` (the skill's directory name) and a
`description`.

## 4. Check it

```sh
homonto package check ./my-engine
homonto package check ./my-workflow ./my-service   # a workflow with the service it needs
```

The check loads the directories the way a config would and runs the same
manifest, resource, skill, agent-rendering and harness checks the shipped
packages pass in `task contracts`. Fix every reported check before
publishing.

## 5. Install it

From a directory, relative to `homonto.toml`:

```toml
[engines.my-engine]
source = "local:./my-engine"
scope  = "project"

[subagents.my-reviewer.opencode]
model = "anthropic/claude-sonnet-5-5"
```

From a published archive (a `.tar.gz` with `framework.toml` at its root),
pinned to its content digest ([remote sources](../guides/remote-sources.md)):

```toml
[engines.my-engine]
source = "remote:https://example.com/my-engine-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"
```

homonto fetches, verifies the digest and caches the archive before anything
reads it. A changed archive under the same URL fails closed until the pin
changes.

## 6. Configure it

```toml
[engines.my-engine.settings]
review = "on-request"
```

`homonto plan` and `homonto explain settings my-engine` show the resolved
values. Your skills read them with `homonto explain settings my-engine --json`.

## Versioning

`manifest_schema` versions the contract itself. A homonto that does not know
a newer schema refuses the package. Your package's own `version` and the
optional `[compat].homonto` range are yours to manage. Renaming an engine
stage breaks routing policies that target it ([routing.md](routing.md)).
