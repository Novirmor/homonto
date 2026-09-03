# homonto

**Declarative configuration for your AI coding tools.**

Describe your MCP servers, skills, commands, subagents, plugins, and settings
once in `homonto.toml`. `homonto apply` projects that desired state into
**OpenCode** through a Terraform-style **plan → confirm → apply** pipeline.
OpenCode is the only adapter; Claude Code and codex support was removed in
v0.13.0 (configs naming them fail at load naming the key).

- **Declarative and reversible.** Edit the TOML. `plan` shows the exact diff,
  `apply` writes it surgically, and removing a resource prunes it on the next
  apply.
- **Secrets are referenced, never stored.** `${pass:…}` and `${ENV_VAR}`
  tokens resolve only at apply time. State keeps a hash, never a value.
- **Surgical merge.** homonto writes only the keys it manages and preserves
  every key you configured by hand, byte for byte.
- **Pinned remote content.** A `remote:` source requires a sha256 digest and
  is verified fail-closed before anything touches your tools.

The repository ships **three binaries**:

| Binary | Role |
|---|---|
| `homonto` | The deterministic installer and projector described above. |
| `onto` | A spec-driven workflow operator. It gates a change through `open → design → build → verify → close` with evidence-based, non-skippable transitions. `onto handoff --json`/`--write` emit versioned recovery packs, `onto evidence record` and `onto trace` keep requirement-to-evidence traceability, and `onto graph` maps change dependencies. |
| `to` | A minimal coding-framework bookkeeper: `plan → do → done`, no gates. The lightweight, mutually exclusive alternative to onto (see [the design](docs/to-framework-design.md)). `to promote` converts a growing `to` change into a full onto change. |

## What the bundled catalog ships

homonto installs content it bundles (`builtin:`), content from your repo
(`local:`), or pinned remote archives (`remote:`). The bundled catalog carries
only what homonto authors:

- **`onto`** — the native, binary-enforced workflow framework: skills, slash
  commands, the shared `homonto` knowledge skill, and five agents (the `onto`
  primary orchestrator plus four specialists).
- **`to`** — the native minimal coding framework for LLMs: a dispatcher, three
  phase skills, `to-no-slop`, the shared `homonto` knowledge skill, and five
  agents (the `to` primary orchestrator plus four specialists). onto and
  `to` are an exclusive choice; declaring both is a config error.
- **Loose skills and commands** (`handoff`, `grilling`, …) — framework-agnostic
  and installed individually.

Third-party workflow stacks are not bundled. As of v0.3.0 the `comet`,
`openspec`, and `superpowers` frameworks are removed
([ADR 0015](docs/adr/0015-ship-only-onto-frameworks.md)); vendor such content
through a `local:` framework or a digest-pinned `remote:` source.

## Install

```bash
go install github.com/noviopenworks/homonto@latest           # homonto
go install github.com/noviopenworks/homonto/cmd/onto@latest  # onto (optional)
go install github.com/noviopenworks/homonto/cmd/to@latest    # to (optional)
```

Tagged releases attach prebuilt `homonto`, `onto`, and `to` binaries for
Linux, macOS, and Windows (amd64 and arm64) with a `SHA256SUMS` file. From a
checked-out repo use `go install .`, not a bare `go build .`: the output name
collides with the `homonto/` content directory (see
[troubleshooting](docs/guides/troubleshooting.md)).

After installing a newer binary, run `homonto update` to bring the projected
catalog content (frameworks, skills, commands, subagents) up to that version.

## First steps

```bash
homonto init            # scaffold homonto.toml, .gitignore, .env.example, homonto/skills/
$EDITOR homonto.toml    # declare your MCPs / skills / plugins / settings
homonto plan            # dry run: show the diff, write nothing, resolve no secrets
homonto apply           # plan → confirm [y/N] → write atomically (--yes to skip)
homonto status          # afterwards: report drift / pending / clean
```

A small but realistic config:

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]       # projected into OpenCode by default

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env = { BRAVE_API_KEY = "${pass:ai/brave}" }    # a reference, never a literal secret
targets = ["opencode"]                          # the only valid target

[skills.my-notes]
source = "local:my-notes"                       # → homonto/skills/my-notes/
scope = "project"                               # required: user | project

[settings.opencode]
model = "anthropic/claude-opus-4-8"
```

`plan` prints a Terraform-style diff (`+` create, `~` update, `-` delete).
`apply` resolves every secret up front and aborts before any write if one
fails, then writes each file atomically while keeping every key it does not
manage.

**New to homonto?** Start with the
[getting-started guide](docs/guides/getting-started.md): a hands-on
walkthrough with real command output and a supported / not-supported matrix.

## Commands at a glance

| Command | What it does |
|---|---|---|
| `homonto init [dir]` | Scaffold a starter repo (never overwrites existing files). |
| `homonto plan` | Show what apply would change. Writes nothing. |
| `homonto apply` | Project the config into the tools, after confirmation. `--snapshot` journals it as an undoable transaction. |
| `homonto explain [kind] [name]` | Why each managed resource exists: origin, destination, last change, removal. |
| `homonto snapshot undo <id>` | Reverse a committed snapshot apply (refuses over user edits). |
| `homonto snapshot recover <id>` | Roll back an interrupted snapshot apply. |
| `homonto permissions suggest` | Render a `bash_allow_add` snippet from approved commands (writes nothing). |
| `homonto status` | Report drift (disk changed outside homonto) vs. pending (unapplied edits). |
| `homonto doctor` | Health check: `pass` present, tool dirs, skill content and links, incomplete snapshots. |
| `homonto update` | Re-materialize the embedded catalog at this binary's version and re-project it. |
| `homonto cache gc` | Reclaim unreferenced remote-cache entries. |

Full flags, exit codes, and examples:
[homonto CLI reference](docs/guides/cli-reference.md) ·
[onto CLI reference](docs/guides/onto-reference.md) ·
[to reference](docs/guides/to-reference.md).

## Documentation

| Guide | What it covers |
|---|---|
| [Getting started](docs/guides/getting-started.md) | First steps with real output. **Start here.** |
| [Configuration reference](docs/guides/configuration.md) | Every `homonto.toml` table and field, defaults, and validation rules. |
| [homonto CLI reference](docs/guides/cli-reference.md) | Every command, flag, exit code, and example. |
| [Secrets](docs/guides/secrets.md) | `${pass:…}` / `${ENV_VAR}` references and the never-stored guarantees. |
| [Projection & state](docs/guides/projection-and-state.md) | Surgical merge, symlinks, drift vs. pending, adoption, pruning. |
| [Subagents](docs/guides/subagents.md) | The `[subagents.*]` resource: sources, link vs. copy, the `homonto:` block. |
| [Remote source trust](docs/guides/remote-source-trust.md) | Pinned, fail-closed remote installs: threat model and lifecycle. |
| [The onto workflow](docs/guides/onto-workflow.md) | Concepts: phases, skills, specialist subagents. |
| [onto reference](docs/guides/onto-reference.md) | Every onto command and every gate the binary enforces. |
| [The to workflow](docs/guides/to-workflow.md) | Concepts: `plan → do → done`, the plan contract, the subagents. |
| [to reference](docs/guides/to-reference.md) | Every `to` command: the gate, flags, archive naming, crash safety. |
| [Enforcement](docs/guides/enforcement.md) | Making the workflow non-skippable with tool hooks (`onto doctor --quiet` / `to doctor --quiet`). |
| [YAGNI](docs/guides/yagni.md) · [KISS](docs/guides/kiss.md) | The principles both frameworks enforce: what to build, and how simply. |
| [Troubleshooting & caveats](docs/guides/troubleshooting.md) | Known limitations and gotchas, with workarounds. |

## Caveats (the short list)

homonto is a young, narrow tool. The most important limitations, each detailed
in [troubleshooting](docs/guides/troubleshooting.md):

- **OpenCode JSONC comments** are dropped by any apply that writes
  `opencode.jsonc`. A no-op apply leaves the file untouched.
- **Secrets need a backend:** `${pass:…}` requires `pass` on `PATH`;
  `${ENV_VAR}` requires the variable set at apply time.
- **Project links survive repository moves.** Same-domain project symlinks
  carry relative targets (ADR 0026); a link stranded by a wholesale rename is
  repaired through the normal plan/confirm path.
- **CLI output goes to stderr.** Redirect with `2>&1` when scripting.

## For contributors

The source of truth for shipped behavior is the code and its tests. Durable
architecture rationale lives in [`docs/adr/`](docs/adr/). Start with
[`AGENTS.md`](AGENTS.md) for how work is done here: directly on a branch, with
no external workflow stack
([ADR 0023](docs/adr/0023-develop-directly-without-comet.md)). onto is the
workflow we ship, and [`docs/personas.md`](docs/personas.md) explains the
split. Releases follow
[`docs/release-checklist.md`](docs/release-checklist.md).
