# homonto documentation

## Start here

1. [Installation](installation.md) — mise (recommended), the installer,
   release archives, or Go.
2. [Getting started](getting-started.md) — scaffold, plan, apply, and run one
   change through each engine, with real output.
3. [Concepts](concepts.md) — the mental model and a glossary.
4. [Examples](examples.md) — 24 copy-ready configs and sessions.

## Configure

| Guide | Covers |
|---|---|
| [Configuration](configuration.md) | Every `homonto.toml` table and field |
| [Catalog](catalog.md) | Every package, skill, command, agent, and plugin — and what it's for |
| [Subagents and permissions](subagents.md) | Agent files, model blocks, permission rendering, what shipped agents may do |
| [Secrets](secrets.md) | `${pass:…}` and `${ENV}` references |
| [Projection and state](projection-and-state.md) | How apply writes, drift vs. pending, adoption, pruning, snapshots |
| [Remote sources](remote-sources.md) | Pinned, verified `remote:` installs |
| [OpenCode integration](opencode.md) | Workflow plugin, status panel, GitHub draft tools, terminal settings, V1 upgrade |

## Work

| Guide | Covers |
|---|---|
| [Workflows](workflows.md) | Choosing onto / to / no / h, routing, and the behavior they share |
| [onto workflow](onto-workflow.md) | The evidence-gated lifecycle, phase by phase |
| [to workflow](to-workflow.md) | The minimal plan → do → done loop |
| [no engine](no-workflow.md) | Implement and review without records |
| [h GitHub workflow](h-workflow.md) | Issues and pull requests |
| [Workspaces](workspaces.md) | Several repositories, worktrees, managed records, workspace tasks |
| [Cleaning up terminal workflows](workflow-cleanup.md) | Archive abandoned records and remove finished worktrees safely |
| [Principles](principles.md) | YAGNI and KISS as the skills apply them |

## Look up

| Reference | Covers |
|---|---|
| [homonto CLI](cli-reference.md) | Every `homonto` command and flag |
| [onto CLI](onto-reference.md) | Every `onto` command and gate |
| [to CLI](to-reference.md) | Every `to` command |
| [Troubleshooting](troubleshooting.md) | Symptoms and fixes |

## Beyond the guides

- [`../adr/`](../adr/) — architecture decision records: why things are the
  way they are.
- [`../to-framework-design.md`](../to-framework-design.md) — the design
  interview behind `to`.
- [`../release-checklist.md`](../release-checklist.md) — how releases are
  cut.
- [`../../CHANGELOG.md`](../../CHANGELOG.md) — release history.
- [`../../AGENTS.md`](../../AGENTS.md) and [`../agents/`](../agents/) —
  developing homonto itself.
