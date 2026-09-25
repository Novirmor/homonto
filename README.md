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
- **Shared workflow coordinator.** The bundled `homonto` primary chooses
  routine workspace, isolation, and validation details from repository evidence
  instead of pausing for approval; it asks only about product intent, required
  waivers, or destructive ambiguity.
- **Declared multi-repo access.** `[repos]` names trusted sibling Git
  worktrees. `homonto apply` gives the shared `homonto` coordinator and
  implementers OpenCode access to those paths, while undeclared directories and
  read-only specialists stay outside that boundary.

### OpenCode 2 early preview

**v0.32.0-rc.1** is an evaluation prerelease, not a production
support guarantee. Its default runtime targets OpenCode 2: builtin
`onto`, `to`, and `h` install the V2 workflow server and terminal plugin.
Supported V1 server configuration syntax remains accepted by OpenCode 2; a
wholesale JSON rewrite is not required. Explicitly enabling the old
`workflow_bridge` still loads a V1 plugin and is **not** V2-compatible. `plan`
and `doctor` report that opt-in and flag external plugins as unverified; these
are advisory findings, not a successful runtime compatibility test.

**What has been checked:** the Go suite, focused race tests, V2 TypeScript
contracts, and an isolated OpenCode 2.0.16 server with a local model and fake
`gh` implementation. The sandbox covered plugin activation, the permission →
native-question → exact-shell-permission flow, denial, and one simulated send
with a validated receipt. **No external model call or real GitHub comment was
made.** The full pre-tag gate, including Docker packaging, passed locally.
A real GitHub sandbox and live V2 terminal notification/reload checks remain
unverified; this limited prerelease proceeds with those gaps explicitly
accepted, not as a production-support claim. See the
[release notes](docs/release-notes.md) and
[release checklist](docs/release-checklist.md).

#### Read-only workflow context and panel

```toml
[integrations.opencode]
workflow_context = true
```

This declaration is needed only when no builtin workflow framework is present.
`workflow_context` defaults to true for builtin `onto`, `to`, and `h` frameworks
and false otherwise. Explicitly set it to false to opt out. The old bridge is
not installed by default. An explicitly true `workflow_bridge`, or an enabled `[plugins.opencode.<name>]` with
`source = "homonto-workflow"`, conflicts with this option: explicitly disable or
remove that declaration before applying. homonto never silently disables an
explicit plugin. The bundled `permission-observer` can be declared separately.

Apply installs the project-local relative directory link
`.opencode/plugins/homonto-workflow-context` →
`../../.homonto/catalog/plugins/homonto-workflow`, even without a framework.
Its `index.ts` is the V2 server entrypoint and `tui.tsx` is the terminal entrypoint.
An older homonto-owned standalone `.ts` context link is retired on apply.
It uses the same `binding.json` selected-config identity as the legacy bridge,
so a nested launch directory does not select a different config. Switching
between bridges removes the inactive homonto-owned link; foreign paths are
preserved and reported as conflicts. Disabling `workflow_context` does not
automatically restore the V1 bridge; that requires an explicit legacy opt-in.

For a preview, keep a backup of V1 config and state, install matching versions
of `homonto`, `onto`, and `to`, inspect `homonto plan`, then run `homonto apply`.
Quit and restart OpenCode after the apply. Do not point V1 OpenCode at V2-only
terminal settings; restoring a V1 setup requires its original config, not only
a homonto snapshot undo.

The V2 bridge supplies read-only workflow snapshot context for model requests
and compaction only when the session's project matches the plugin's project
and the session directory's realpath is within the bound config root. Detached
external worktrees do not receive context yet. In a V2 terminal session, open
the command palette and select **Open workflow status**; the panel displays a
fresh bounded snapshot with health findings and exact-generation recovery
pointers. Press `r` in the panel to refresh. The terminal uses its connected
server's read-only RPC; it does not read the terminal machine's local files,
which matters when the server is remote. With the builtin `h` framework, the
server registers V2 GitHub draft tools using native question forms and a
bound-service permission request; authorization fails closed if that service
cannot be verified. It also emits session-scoped terminal notifications on
idle observation. It provides no workflow mutations or verification authority.
With `h` installed, readiness reports the live-host validation requirement. The
Go workflow commands remain available; injected context is not authorization
to act or evidence that work passed verification.

#### Remaining runtime work

V2 has verified alternatives to several V1 surfaces:
[local plugin discovery](https://opencode.ai/v2/docs/plugins/#discover) loads
direct `.ts` files under `.opencode/plugins/`, and the V2.0.16
[full client](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/client/src/promise/generated/client.ts)
exposes resource-specific `permission.create`. The
[injected permission context](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/plugin/src/promise/permission.ts)
exposes only `list`, `get`, `reply`, and the evaluation hook, not `create`.
The V2 GitHub tools use the documented local service registration and HTTP
API, check an instance-specific RPC
identity, and treat a matched permission reply and correlated native form answer
as separate authorization steps. Connecting to a standalone server without a
registered local service leaves those tools unavailable rather than bypassing
the permission gate. The terminal receives notifications through connected RPC.

The V2 `permission-observer` suggests only when two correlated **`once`** replies
approve the same exact single-resource shell command. The V2.0.16
[shell producer](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/core/src/tool/plugin/shell.ts)
does not attach the exact command. The observer correlates the request's tool
source with the command observed in the tool hook and requires an identical
single shell resource. The
[permission producer](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/core/src/permission.ts)
emits indistinguishable `always` replies for some automatic approvals, so these
do not count toward the threshold. Execution is not approval. Full release
readiness requires verification against the installed V2 release and a real
GitHub sandbox before production publication. In an isolated V2.0.16 server,
the read-only panel and permission → native question → shell permission →
receipt flow were exercised with a local model and a fake `gh` binary; denial
never sent, and allowing both approvals sent once to the fake destination.
No external GitHub publication or external model call was made.

The next V2-native opportunities are not implemented yet:

- **Publication transaction:** consolidate the existing draft/approval tools
  into one independently verified V2 preview → approve → publish operation;
  until then the bound-service permission and native question must both succeed.
- **Worktree-aware sessions:** associate a session with an explicitly selected
  workflow generation and declared checkout. Extend the current containment
  guard only with verified bindings, and invalidate context when sessions move.
- **Watcher notifications:** extend the current session-idle notifications to
  debounced filesystem changes without losing headless observation.

#### Terminal settings

Terminal settings now target **OpenCode V2 only**. Keep the `[tui.opencode]`
TOML surface; it projects to global `$XDG_CONFIG_HOME/opencode/cli.json`, or
`~/.config/opencode/cli.json` when XDG_CONFIG_HOME is unset. There is no
project-local terminal settings projection.

XDG support here is limited to the terminal file; existing server configuration
and resource projection still use `~/.config/opencode`.

```toml
[tui.opencode]
theme = { name = "gruvbox", mode = "system" }
scroll = { speed = 3, acceleration = false }
tabs = { mode = "auto" }

[tui.opencode.keybinds]
"app.exit" = ["ctrl+c", "<leader>q"]
"help.show" = false
```

Supported native groups follow the [V2 CLI reference](https://opencode.ai/v2/docs/cli/config/):
theme, cursor, scroll, leader, prompt, session, tabs, diffs, attention, terminal,
mini, keybinds, debug, and experimental, plus animations, mouse, and `$schema`.
Keybinding command IDs containing dots must be quoted in TOML.
Unknown fields and invalid values fail before projection. Terminal plugin
declarations are outside this projection and are rejected.

Four legacy forms have direct mappings: string `theme` → `theme.name`,
`scroll_speed` → `scroll.speed`, `scroll_acceleration.enabled` →
`scroll.acceleration`, and `leader_timeout` → `leader.timeout`. Declaring both
an alias and its native destination is an error. Other V1-only settings, including
`diff_style`, `attention.enabled`, and old keybind IDs such as `app_exit`, require
an explicit native V2 replacement; they are never copied blindly.

Only declared settings are migrated. Unmanaged `cli.json` values, including
nested sibling settings and plugins, survive apply and prune. Global and
project V1 `tui.json(c)` files remain untouched. If you want OpenCode to migrate
other V1 preferences automatically, start V2 before homonto creates `cli.json`.

Old `tui.*` state records are retired without deleting V1 file content; new
records use `tui.cli.*` and manage individual native settings. Restoring old
state requires another V2 plan, and replaying historical V1 write changes is
rejected rather than redirected into `cli.json`. The existing snapshot engine
re-plans current TOML and restores checkpoint state: **snapshot undo does not
reverse this file migration**. To restore prior terminal values, declare them
and apply again. Supported V1 `opencode.json(c)` settings,
`mcp` entries, and `plugin` arrays keep their existing format. Native `plugins`
in `[settings.opencode]` is reserved to prevent overriding managed plugin
declarations. Runtime plugin migration is separate. See the
[V1 migration guide](https://opencode.ai/v2/docs/migrate-v1/).

The repository ships **three binaries**:

| Binary | Role |
|---|---|
| `homonto` | The deterministic installer and projector described above. |
| `onto` | A spec-driven workflow operator: `open → design → build → verify → close`. Normal transitions validate required artifacts, recorded evidence, and applicable Git state. `onto handoff --json`/`--write` emit versioned recovery packs, `onto evidence record` and `onto trace` keep requirement-to-evidence traceability, and `onto graph` maps change dependencies. |
| `to` | A minimal coding-framework bookkeeper: `plan → do → done`. It checks installation, phase/state, and applicable source cleanliness; `done --verified` records a self-asserted verification claim. The skills require real verification (see [the design](docs/to-framework-design.md)). `to promote` converts a growing `to` change into a full onto change; `onto demote` converts back. |

### Onto Task And Trace IDs

An onto task that participates in structured evidence uses both identifiers:

```md
- [ ] 1.1 Implement the parser [trace #1]
```

`1.1` pairs the `tasks.md` checkbox with `## Task 1.1` in `plan.md`; `#1`
identifies the task to `onto trace` and `onto evidence record --task 1`.
Trace IDs are positive, unique within the change, and never renumbered. Legacy
`- [ ] #1 ...` tasks still parse, but new workflow tasks use the combined form.

## What the bundled catalog ships

homonto installs content it bundles (`builtin:`), content from your repo
(`local:`), or pinned remote archives (`remote:`). The bundled catalog carries
only what homonto authors:

- **`onto`** — the native, binary-enforced workflow framework: skills, slash
  commands, the shared `homonto` knowledge skill, the shared `homonto`
  coordinator, and four specialists.
- **`to`** — the native minimal coding framework for LLMs: a dispatcher, three
  phase skills, `to-no-slop`, the shared `homonto` knowledge skill, the shared
  `homonto` coordinator, and four specialists. onto and `to` are complementary;
  declare either or both and pick the workflow per change through `/onto` or
  `/to` ([ADR 0042](docs/adr/0042-onto-and-to-are-complementary.md)).
- **`h`** — the GitHub skill bundle: `h-spike-issue`, `h-resolve-issue`,
  `h-review-pr`, `h-continue-pr`, and `h-review-batch`, with matching `/h-*`
  commands and two read-only workers, `h-spike` and `h-review`. It installs
  both onto and to as dependencies and uses the shared `homonto` coordinator.
- **Loose skills and commands** (`handoff`, `grilling`, …) — framework-agnostic
  and installed individually.

`[frameworks.h]` with `source = "builtin:h"` remains the stable configuration
key: `frameworks` is the generic package/dependency mechanism, including skill
bundles. There is no key rename or third lifecycle workflow. `/onto`, `/to`,
and `/h-*` all route to the shared `homonto` coordinator
([ADR 0045](docs/adr/0045-one-homonto-coordinator-for-both-workflows.md)).

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

No Go toolchain? On Linux/macOS (amd64/arm64) download and run the
interactive installer: it asks which binaries you want, verifies the release
archives against `SHA256SUMS`, installs into a directory you choose, and
prints the PATH line for you to apply — it never edits your shell
configuration. It can also run the non-destructive `homonto init` in the
current directory when you explicitly confirm. For a new config, it also asks
where workflow records belong, which sibling Git repositories to trust, which
onto/to lifecycle workflows to enable, and whether to add the `h` GitHub skill
bundle when both workflow binaries are installed. Its model choice is written
to `[settings.opencode]` and every selected agent's model block: on apply, it
updates **global OpenCode settings**, affecting other projects too. Next steps
name only the configured, installed workflows and, when selected, `/h-*`.
Existing `homonto.toml` files remain unchanged. In an
interactive terminal it uses Gum when available, then dialog, then text
prompts:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/noviopenworks/homonto/main/scripts/install.sh && bash install.sh
```

Tagged releases attach prebuilt `homonto`, `onto`, and `to` binaries for
Linux, macOS, and Windows (amd64 and arm64) with a `SHA256SUMS` file. From a
checked-out repo, `go install .` installs the binary. If you have created a local
`homonto/` content directory, use an explicit output path when building to avoid
a name collision (see [troubleshooting](docs/guides/troubleshooting.md)).

After installing a newer binary, run `homonto update` to bring the projected
catalog content (frameworks, skills, commands, subagents) up to that version.

## First steps

Run these commands in the directory that will hold `homonto.toml`. `homonto
init` scaffolds configuration only: it never runs `git init`, and no MCP server
is required. Add MCPs only when a tool needs one; framework installation is a
separate, declarative `[frameworks.onto]` or `[frameworks.to]` entry (or both
through `[frameworks.h]`) followed by `homonto apply`. A `[tmp]` entry declares
one gitignored scratch directory every agent can write to without prompts —
apply creates it and generates the
skill reference that names it
([ADR 0048](docs/adr/0048-one-declared-workspace-tmp-directory.md)).

```bash
homonto init            # scaffold homonto.toml, .gitignore, .env.example
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

# One scratch directory every agent can write to without prompts. apply
# creates it, keeps it gitignored, and generates the skill reference that
# names it; homonto never deletes its content ([ADR 0048](docs/adr/0048-one-declared-workspace-tmp-directory.md)).
# [tmp]
# dir = ".tmp"
```

For this example's optional local skill, create the source before `plan` or
`apply`; `init` does not create `homonto/skills/` or a `.gitkeep`:

```bash
mkdir -p homonto/skills/my-notes
$EDITOR homonto/skills/my-notes/SKILL.md   # write your skill instructions
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
| `homonto init [dir]` | Scaffold a starter config directory (never overwrites files or initializes Git). |
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
| [to reference](docs/guides/to-reference.md) | Every `to` command: installation and state checks, verification assertions, archive naming, crash safety. |
| [Enforcement](docs/guides/enforcement.md) | Read-only doctor diagnostics and the OpenCode workflow observer; their limits at the tool boundary. |
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
([ADR 0023](docs/adr/0023-develop-directly-without-comet.md)). onto and to are the
lifecycle workflows we ship, with the `h` GitHub skill bundle;
[`docs/personas.md`](docs/personas.md) explains the
split. Releases follow
[`docs/release-checklist.md`](docs/release-checklist.md).
