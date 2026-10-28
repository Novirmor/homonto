# homonto

**Declarative configuration and agent workflows for OpenCode.**

Describe your MCP servers, skills, commands, agents, plugins, and settings once
in `homonto.toml`. homonto shows you the diff, asks, and writes exactly that
into OpenCode — and removes it again when you delete it. On top, it ships
ready-made agent workflows that take a change from request to a verified,
integrated result.

```console
$ homonto plan
opencode:
  + projmcp.codegraph = {"command":["codegraph","serve","--mcp"],"enabled":true,"type":"local"}
  + skill.house-style = …/.opencode/skills/house-style -> ../../homonto/skills/house-style
$ homonto apply
Apply these changes? [y/N] y
Applied.
```

## Why

- **Plan, then apply.** Every change is a reviewable diff (`+` `~` `-`)
  before anything is written. Writes are atomic.
- **Surgical.** homonto touches only the keys it manages. Your hand-written
  OpenCode settings survive.
- **Reversible.** Delete a declaration and the next apply prunes it. Drift
  from out-of-band edits is detected and reported.
- **Secrets stay out.** `${pass:…}` and `${ENV}` references resolve only at
  apply time; state keeps a hash, never a value.
- **Pinned remote content.** `remote:` sources need a sha256 digest and are
  verified before anything is written.
- **Workflows included.** Evidence-gated (`onto`), minimal (`to`), record-free
  (`no`), and GitHub (`h`) workflows, run by one coordinator agent with
  read-only reviewers and skeptics.

## Install

With [mise](https://mise.jdx.dev/) (2026.10.19 or newer):

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@latest'
homonto version
```

All three binaries — add to `mise.toml` (project) or `~/.config/mise/config.toml`:

```toml
[tool_alias]
homonto = "github:Novirmor/homonto"
onto    = "github:Novirmor/homonto"
to      = "github:Novirmor/homonto"

[tools]
homonto = { version = "latest", matching_regex = "^homonto_" }
onto    = { version = "latest", matching_regex = "^onto_" }
to      = { version = "latest", matching_regex = "^to_" }
```

```sh
mise install
```

Other ways — interactive installer, release archives, `go install`:
[installation guide](docs/guides/installation.md).

## Quick start

```sh
homonto init           # homonto.toml, .gitignore, .env.example — nothing else
```

```toml
# homonto.toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]
scope   = "project"

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env     = { BRAVE_API_KEY = "${pass:ai/brave}" }

[engines.to]                       # the minimal workflow engine
source = "builtin:to"
scope  = "project"

# every installed agent names its model
[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```sh
homonto plan           # dry run: the diff, no writes, no secrets resolved
homonto apply          # confirm, then write
homonto status         # later: drift or pending edits?
```

Then, in OpenCode:

```text
/to add a --json flag to the list command
```

Walkthrough with real output: [getting started](docs/guides/getting-started.md).
More configs: [examples](docs/guides/examples.md).

## What's inside

Three binaries from one release:

| Binary | Role |
|---|---|
| `homonto` | Projects `homonto.toml` into OpenCode; manages state, workspaces, and worktrees. |
| `onto` | Owns onto change state and refuses transitions that lack their evidence. |
| `to` | Bookkeeper for to changes: phase, archive, status, handoff. |

Four packages in the bundled catalog:

| Package | Declare | What you get |
|---|---|---|
| **onto** | `[engines.onto]` | `open → design → build → verify → close`, plus `fix` and `tweak` presets. Proposals, designs, delta specs, task plans, verification evidence, ≥2 skeptics, receipt-bound integration. For work someone else must pick up or audit. |
| **to** | `[engines.to]` | `plan → do → done` with one `plan.md`. Real verification and a skeptic pass, recorded as a self-asserted claim. For fast focused work. |
| **no** | `[engines.no]` | An implementer and a reviewer. No binary, no records. |
| **h** | `[workflows.h]` | `/h-spike-issue`, `/h-resolve-issue`, `/h-review-pr`, `/h-continue-pr`, `/h-resolve-conflicts`, `/h-review-batch`. Comments and reviews require approval of the shown draft; conflict resolution authorizes only its verified same-PR push. |

Plus standalone skills and commands (`handoff`, `grilling` / `/grill`) and an
OpenCode 2 plugin that shows workflow status in a panel and carries workflow
context through compaction. Every item and its purpose:
[catalog](docs/guides/catalog.md).

### How the workflows run

```text
you ─▶ homonto (coordinator) ─┬─▶ implementers         edit + test, one per checkout
                              └─▶ explorers, reviewers, read-only, many in parallel
                                  skeptics, h workers
```

One coordinator agent asks you once which workflow fits new work, then drives
it to the endpoint — implementation, review, verification, archive,
integration, and authorized publication — and only stops for a decision that
is yours, an endpoint you named, or a real blocker. It owns every state
change, commit, and publication; specialists never ask you questions.
Details: [workflows](docs/guides/workflows.md).

## Commands

| Command | Does |
|---|---|
| `homonto init [dir]` | Scaffold a config. |
| `homonto plan` | Show the diff. `--exit-code` for CI. |
| `homonto apply` | Write it. `--snapshot` makes it undoable. |
| `homonto status` | Drift vs. pending. |
| `homonto doctor` | Environment and link health. |
| `homonto explain [kind] [name]` | Why a resource exists and where it came from. |
| `homonto update` | Bring installed catalog content to this binary's version. |
| `homonto workspace …`, `homonto worktree …` | Multi-repository records, worktrees, tasks. |

Full references: [homonto](docs/guides/cli-reference.md) ·
[onto](docs/guides/onto-reference.md) · [to](docs/guides/to-reference.md).

## Documentation

| | |
|---|---|
| [Installation](docs/guides/installation.md) | mise, installer, archives, Go |
| [Getting started](docs/guides/getting-started.md) | First config to first change |
| [Concepts](docs/guides/concepts.md) | Mental model and glossary |
| [Examples](docs/guides/examples.md) | 24 configs and sessions |
| [Configuration](docs/guides/configuration.md) | Every field |
| [Catalog](docs/guides/catalog.md) | Every skill, command, and agent |
| [Workflows](docs/guides/workflows.md) | onto, to, no, h — choosing and behavior |
| [Workspaces](docs/guides/workspaces.md) | Several repositories and worktrees |
| [Troubleshooting](docs/guides/troubleshooting.md) | Symptoms and fixes |

Everything else: [docs index](docs/guides/README.md).

## Scope and limits

- **OpenCode only.** Claude Code and Codex support was removed in v0.13.0;
  configs naming them fail to load.
- **OpenCode 2 by default.** Supported V1 config keeps working; see
  [OpenCode integration](docs/guides/opencode.md).
- Applies that rewrite `opencode.jsonc` drop its comments.
- Workflow permissions trust the workspace; they are not a sandbox.
- CLI output goes to stderr.

## Contributing

The code and its tests define behavior; durable rationale is in
[`docs/adr/`](docs/adr/). Start with [`AGENTS.md`](AGENTS.md), run `task` for
the developer tasks, and `task check` before pushing. This repository is
developed directly on branches — it does not use its own workflows on itself
([ADR 0023](docs/adr/0023-develop-directly-without-comet.md)); onto and to are
verified by their conformance and end-to-end test suites. Releases follow
[`docs/release-checklist.md`](docs/release-checklist.md); history is in
[`CHANGELOG.md`](CHANGELOG.md).

## License

[MIT](LICENSE)
