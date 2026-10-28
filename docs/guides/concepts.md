# Concepts

The mental model behind homonto, in the order you meet it.

## One config, projected

```text
homonto.toml ──plan──▶ diff ──apply──▶ OpenCode files
     ▲                                     │
     └────────── status / doctor ◀─────────┘
```

You describe the desired state of your AI tooling in **`homonto.toml`**:
MCP servers, skills, slash commands, agents, plugins, settings, and workflow
packages. homonto **projects** that description into the tool's own files.

- **`plan`** computes the diff and prints it. It writes nothing and resolves
  no secrets.
- **`apply`** shows the same diff, asks `[y/N]`, resolves secrets, and writes.
- **`status`** compares disk against the last apply (drift) and the config
  against the last apply (pending).
- **`doctor`** checks the environment: secret backends, tool directories,
  links, declared repositories, tooling providers.

OpenCode is the only target tool ("adapter"). Claude Code and Codex support
was removed in v0.13.0; configs that name them fail to load.

## Managed, not owned

homonto writes **only the keys it manages** and leaves the rest of a file
alone. Your hand-written settings in `opencode.jsonc` survive every apply.
State in `.homonto/state.json` remembers what homonto wrote, so:

- removing a resource from the config **prunes** exactly what homonto wrote;
- a hand-made resource that exactly matches a declaration is **adopted** into
  state silently;
- a managed value changed outside homonto is reported as **drift** and reset
  on the next apply.

Content homonto installs (skills, commands, agents) is **symlinked** from a
source directory, so edits to the source are live. See
[projection and state](projection-and-state.md).

## Sources

Every skill, command, agent, and package names where its content comes from:

| Source | Meaning |
|---|---|
| `builtin:<name>` | Shipped inside the binary (the *catalog*), materialized to `.homonto/catalog/`. |
| `local:<name>` | Your content next to `homonto.toml`, under `homonto/`. |
| `remote:<url>` | A fetched archive pinned by `digest = "sha256:…"`, verified before use. |

## Scope

`scope = "project"` projects into the repository (`./.opencode/`,
`./opencode.jsonc`). `scope = "user"` projects into your global OpenCode
config (`~/.config/opencode/`), affecting every project. Skills, commands,
and packages require an explicit scope; MCP servers default to `user`, agents
to `project`.

## The catalog

The catalog is the set of `builtin:` content compiled into the binary:
packages, skills, slash commands, agents, and plugins. It is versioned with
the binary. After upgrading the binary, `homonto update` re-materializes it.
The [catalog guide](catalog.md) describes every item.

## Packages: workflows and engines

A **package** bundles skills, commands, agents, and plugins that only make
sense together. Declaring one installs all of it. Packages come in two roles:

| Role | Table | Builtin | What it provides |
|---|---|---|---|
| **Engine** | `[engines.<name>]` | `onto`, `to`, `no` | A work model for implementing changes. `onto` and `to` also have a binary that owns lifecycle state. |
| **Workflow** | `[workflows.<name>]` | `h` | User-facing coordination that uses engines, e.g. GitHub issue and PR work. |

Engines can coexist. Each **task** uses exactly one engine and keeps it on
resume. `h` needs no engine to research issues or review PRs; resolving an
issue needs one installed.

## The coordinator and its specialists

The `onto`, `to`, and `h` packages all install the same primary agent,
**`homonto`** — the coordinator. Every `/onto*`, `/to*`, and `/h-*` slash
command runs in it. The coordinator:

- chooses the engine with you (once, for new work), then follows that engine's
  dispatcher skill;
- owns every workflow state change, commit, integration, and publication;
- delegates bounded work to **specialists** and checks what they return.

Specialists come in two kinds:

| Kind | Agents | Permissions | Concurrency |
|---|---|---|---|
| Read-only | explorers, reviewers, skeptics, `h-spike`, `h-review`, `no-reviewer` | no edits, no shell | many in parallel |
| Writers | `onto-implementer`, `to-implementer`, `no-implementer` | edits + trusted shell; no push/publish/workflow commands | one per checkout; parallel across repositories |

Subagents never ask you questions. They return `Questions:` or
`Evidence requests:` to the coordinator, which investigates or asks you once.

## Lifecycle state belongs to binaries

`onto` and `to` keep each change's phase in a state file (`onto-state.yaml`,
`to-state.yaml`) that only the binary writes. The binary also refuses
transitions whose requirements are missing. Skills carry the *process*; the
binary carries the *state and gates*. No agent hand-edits state.

The **install gate**: mutating `onto`/`to` commands refuse to run until
`homonto.toml` declares the matching `[engines.*]` table and `homonto apply`
has materialized it.

## Workflow root and records

`[workflow] root` (default `docs`) is where lifecycle records live:

```text
docs/
├── changes/           onto: active changes and archive/
├── specs/             onto: living capability specs
├── adr/               onto: accepted decisions
├── guides/            onto: user guides a change must keep current
└── tasks/             to: active changes and archive/
```

`no` writes no lifecycle records.

## Schema 1 vs schema 2 workspaces

A plain config (no `schema_version`, or `1`) treats the config's own Git
checkout as the source repository; `[repos]` adds siblings. With
`schema_version = 2`, every source repository is declared in `[repos]`, the
config directory need not be Git, records can live in their own repository,
and changes can run in registered worktrees. See [workspaces](workspaces.md).

## Tooling providers

The workflows can use a token-saving shell proxy (`rtk`) and a
code-intelligence index (`graphify`, `okf`). Declare them in `[tooling]`;
homonto renders instructions that name only what you declared. It never
installs or runs them.

## Glossary

| Term | Meaning |
|---|---|
| Adapter | The code that projects into one tool. OpenCode is the only one. |
| Adoption | Recording a hand-made, exactly matching resource into state without writing. |
| Archive | Where a finished change's records move: `changes/archive/` or `tasks/archive/`. |
| Candidate | The exact commit that was verified and will be delivered. |
| Cleanup | `onto cleanup` / `to cleanup`: archiving abandoned records and removing clean, integrated worktrees of terminal changes. |
| Config root | The directory containing `homonto.toml`. |
| Coordinator | The `homonto` primary agent. |
| Delta spec | onto's per-change spec edit, merged into `specs/` at close. |
| Dispatcher | The `onto` or `to` skill that discovers state and routes to a phase skill. |
| Drift | A managed value changed on disk outside homonto. |
| Engine | A package providing a work model: `onto`, `to`, `no`. |
| Evidence token | An `onto set …` record the binary requires before a transition. |
| Generation | One lifetime of a change name, with its own `identity`. A name reused after archive or cleanup starts a new generation. |
| Pending | Config edits not yet applied. |
| Preset | onto's reduced paths: `fix` and `tweak`. |
| Receipt | onto's record of how an archived change was integrated (`merge:`, `pr:`, `unchanged:`). |
| Skeptic | A read-only agent prompted to refute a verification claim. |
| Workflow (package) | A user-facing package such as `h`. |
| Workflow root | `[workflow] root`, the records directory. |
