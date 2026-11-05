# Configuration reference — `homonto.toml`

Every table and field, with defaults, rules, and an example for each. For
complete configs by situation, see [examples](examples.md).

`homonto plan`, `apply`, `status`, `doctor`, `explain`, `update`, `workspace`,
`worktree`, and `workflow` read `homonto.toml` from the current directory, or
the file named by `--config <path>`. The directory containing it is the
**config root**; relative paths in the file resolve from there, not from your
shell's working directory.

## Contents

| Table | Declares |
|---|---|
| [`schema_version`](#schema_version) | Config format version (top-level key) |
| [`harnesses`](#general-rules) | The harnesses a declaration without `targets` goes to (top-level key) |
| [`[skills.<name>]`](#skills--skillsname) | Skills |
| [`[commands.<name>]`](#commands--commandsname) | Slash commands |
| [`[subagents.<name>]`](#subagents--subagentsname) | Agent definitions |
| [`[subagents.<name>.opencode]`](#agent-models--subagentsnameopencode) | Each agent's model, variant, step budget, extra allowed commands |
| [`[engines.<name>]`](#engines--enginesname) | Work-model packages: `onto`, `to`, `no` |
| [`[workflows.<name>]`](#workflows--workflowsname) | User-facing packages: `h-workflow-gh`, `h-workflow-gl`, `h-workflow-bb` |
| [`[services.<name>]`](#services--servicesname) | Host or ticket service packages: `host-gh`, `host-gl`, `host-bb` |
| [`[plugins.opencode.<name>]`](#plugins--pluginsopencodename) | OpenCode plugins |
| [`[settings.opencode]`](#settings--settingsopencode) | OpenCode settings |
| [`[tui.opencode]`](#terminal-settings--tuiopencode) | OpenCode terminal settings |
| [`[integrations.opencode]`](#integrations--integrationsopencode) | The bundled workflow plugin |
| [`[tooling]`](#tooling--tooling) | Shell proxy and code-intelligence providers |
| [`[tmp]`](#scratch-directory--tmp) | One shared scratch directory |
| [`[workflow]`](#workflow-records--workflow) | Where lifecycle records live |
| [`[repos]`](#repositories--repos) | Source repositories |
| [`[worktrees]`](#worktrees--worktrees) | Parent directory for registered worktrees |
| [`[integration.<name>]`](#integration-checks--integrationname) | Cross-repository checks |
| [`[routing]`](#model-routing--routing) | Execution-model policies per selector and harness |
| [`[decisions]`](#decision-models--decisions) | Decision-model providers for typed judgments |
| [Package settings and processes](#package-settings-and-processes) | `[<table>.<name>.settings]`, `[<table>.<name>.processes.*]` |
| [`[settings.claude]`, `[subagents.<name>.claude]`](#claude-code--settingsclaude-subagentsnameclaude) | Claude Code settings and agent models |
| [Removed and legacy tables](#removed-and-legacy-tables) | `[mcps]`, `[models]`, `[marketplaces]`, `[agents]`, `[frameworks]`, `[plugins.claude]` |
| [Diagnostics](#diagnostics) | What every load error names |
| [Key index](#key-index) | Every key, generated from the schema |

## General rules

**Everything is optional.** An empty file is valid and projects nothing.

**Validation is strict and fails at load,** naming the offender. Unknown keys
in typed tables are errors, so typos never pass silently:

```console
$ homonto plan
Error: config "…/homonto.toml": parse config: unknown configuration fields:
1| [frameworks.onto]
 |  ~~~~~~~~~~~~~~~ missing table
```

Free-form maps (`[settings.opencode]`, `[tui.opencode]`) are passed through,
subject to reserved keys and the V2 terminal schema.

**Names** must be non-empty and not index-like (`"0"`, `"-1"`), so they can
safely become JSON keys and file names.

**Harnesses and targets.** A top-level `harnesses` list names the harnesses
a declaration goes to when it sets no `targets` of its own; absent means
`["opencode"]`. Valid values are `"opencode"` and `"claude"`
([Claude Code](claude-code.md), ADR 0104). A resource's own `targets` list
wins. `"codex"` fails at load (removed in v0.13.0).

```toml
harnesses = ["opencode", "claude"]
```

**Scope.** `"project"` writes into the config root (`.opencode/`,
`opencode.jsonc`, `.claude/`). `"user"` writes into each harness's global
config (`~/.config/opencode/`, `~/.claude/`) and affects every project.

| Resource | `scope` |
|---|---|
| skills, commands, engines, workflows | required |
| subagents | optional, default `project` |

**Sources.**

| Source | Resolves from | Valid for |
|---|---|---|
| `builtin:<name>` | The catalog in the binary, materialized to `.homonto/catalog/` | skills, commands, subagents, engines, workflows |
| `local:<name>` | `homonto/subagents/<name>.md`, or a package root path | subagents, engines, workflows |
| `remote:<url>` | A fetched archive; requires `digest` ([remote sources](remote-sources.md)) | subagents, engines, workflows |

**Secrets** are written as `${pass:path}` or `${ENV_VAR}` and resolved only
during apply. See [secrets](secrets.md).

## `schema_version`

A top-level key (not a table). Current version: **2**.

```toml
schema_version = 2
```

| Value | Meaning |
|---|---|
| omitted, `0`, `1` | Legacy layout: the config root's Git checkout is the implicit source repository; `[repos]` adds siblings. |
| `2` | Explicit layout: `[repos]` lists every source; config root need not be Git; records may live elsewhere; `workflow.git` and `[worktrees]` are available. |

A version newer than the binary supports fails at load: upgrade the binary.
Changing the version of a config that already has workflow records is
guarded, not migrated. See [workspaces](workspaces.md) before switching.

## Bring your own MCP servers, skills and commands

homonto does not manage MCP servers or your own skills and commands. Put them
where the harness looks: on OpenCode, MCP servers in `opencode.jsonc` (global
or per project), skills in `.opencode/skills/` or `~/.config/opencode/skills/`,
commands in `.opencode/command/` or `~/.config/opencode/command/`; on Claude
Code, servers in `.mcp.json` or `~/.claude.json`, skills and commands under
`.claude/` or `~/.claude/`. `apply` never touches them
([ADR 0085](../adr/0085-bring-your-own-mcp-servers-and-skills.md)). The one
server homonto writes is its own runtime, `mcpServers.homonto` in the config
root's `.mcp.json`
([ADR 0107](../adr/0107-register-the-runtime-mcp-server-as-homontos-own.md)).

These fail at load, naming the fix: any `[mcps.*]` table, an `mcp` key under
`[settings.opencode]`, and a `local:` source on `[skills.*]` or `[commands.*]`.
Builtin skills and commands are still declared below.

Upgrading: the first `apply` releases what an earlier release projected. MCP
entries stay in `opencode.jsonc` byte for byte; links to your own
`homonto/skills/` and `homonto/commands/` content stay where they are. Both
drop out of homonto's state and are yours from then on. `plan` shows each as
a `release`.

## Skills — `[skills.<name>]`

```toml
[skills.handoff]
source = "builtin:handoff"     # a catalog skill
scope  = "user"                # available in every project
```

| Field | Required | Notes |
|---|---|---|
| `source` | yes | `builtin:<name>`. Your own skills are [bring-your-own](#bring-your-own-mcp-servers-skills-and-commands). |
| `scope` | yes | `user` → `~/.config/opencode/skills/`; `project` → `.opencode/skills/`. |
| `repo` | no | A `[repos]` alias; requires `scope = "project"`. |
| `targets` | no | `"opencode"`, `"claude"`. Default: [`harnesses`](#general-rules). |

Skills are **symlinked** from the materialized catalog. Changing `scope`
relocates the link.

Builtin skills you can declare on their own: `handoff`, `grilling`. Package
skills (`onto-*`, `to-*`, `h-*`, `homonto`, `no`) come with their package;
don't declare them individually.

## Commands — `[commands.<name>]`

```toml
[commands.grill]
source = "builtin:grill"     # /grill
scope  = "project"
```

Same fields as skills. A command lands at `.opencode/command/<name>.md` (or
the user-scope equivalent). Packages bring their own commands (`/onto`,
`/to`, `/h-*`, `/no`, …); don't redeclare them. Builtin standalone commands:
`grill`, `handoff`.

## Subagents — `[subagents.<name>]`

```toml
# Your own agent file: projected verbatim (put model: in its frontmatter), or
# rendered like a catalog agent when it has a homonto: block.
[subagents.security-review]
source = "local:security-review"       # → homonto/subagents/security-review.md
scope  = "project"

# A catalog agent under your own name; builtin agents need a model block.
[subagents.audit]
source = "builtin:onto-reviewer"
[subagents.audit.opencode]
model = "anthropic/claude-opus-5-5"
```

| Field | Required | Notes |
|---|---|---|
| `source` | yes* | `builtin:`, `local:`, or `remote:`. *Omit it entirely to [tune a package agent](#tuning-a-package-agent). |
| `scope` | no | `project` (default) → `.opencode/agent/`; `user` → `~/.config/opencode/agent/`. |
| `mode` | no | `link` (default, symlink) or `copy` (managed real file; local edits are backed up to `.bak`). |
| `repo` | no | A `[repos]` alias; project scope only. |
| `digest` | remote only | `sha256:<64 hex>`. |
| `version` | no | Informational. |
| `targets` | no | `"opencode"`, `"claude"`. Default: [`harnesses`](#general-rules). |

Agent file format, permission rendering, and link vs. copy:
[subagents](subagents.md). Legacy `[agents.<name>]` still parses and becomes a
copy-mode subagent.

## Agent models — `[subagents.<name>.opencode]`

**Every rendered agent needs a model** — a builtin agent, whether a package
installs it or you declare it with a `builtin:` source, and a `local:` or
`remote:` agent whose file carries a `homonto:` block. There are no defaults,
tiers, or inheritance. An agent file without a `homonto:` block is projected
verbatim and may **not** have this block; set `model:` in its own frontmatter
instead:

```text
subagents.security-review declares a model override, but local:security-review has no homonto: intent block, so it is projected verbatim and the override could never apply; add a homonto: block to render it, or remove [subagents.security-review.opencode]
```

```toml
[subagents.onto-skeptic.opencode]
model   = "anthropic/claude-opus-5-5"   # required, provider/model
variant = "high"                        # optional provider variant
steps   = 160                           # optional step budget
bash_allow_add = ["make lint"]          # optional exact-command allows
```

| Field | Required | Notes |
|---|---|---|
| `model` | yes | `provider/model`. No control characters, line breaks, or `#variant` suffix. |
| `variant` | no | A provider-defined variant (`high`, `xhigh`, `max`, `thinking`, …). Letters, digits, `.`, `_`, `-`. |
| `steps` | no | Positive integer overriding the agent's iteration budget. |
| `bash_allow_add` | no | Exact commands appended to the agent's allowlist. See below. |

A missing block fails at load:

```text
parse config: subagents.homonto.opencode model is required
```

A setting OpenCode cannot express is an error, not a no-op:

```text
parse config: subagents.onto-reviewer.opencode sets effort "high", but OpenCode has no effort setting — use variant, or drop it
```

Default step budgets of builtin agents: **1200** coordinator, **300**
implementers, **160** `no-reviewer`, **120** other read-only specialists.

homonto checks a model ID's shape, not whether your provider offers it.

### Tuning a package agent

Package agents can't be redeclared (the names would collide). A
`[subagents.<name>.opencode]` block **without** a `[subagents.<name>]`
`source` means "tune the installed agent":

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[subagents.onto-reviewer.opencode]    # tunes the package's onto-reviewer
model = "anthropic/claude-opus-5-5"
steps = 180
```

Tuning an agent nothing installs is an error:

```text
parse config: subagents.onto-skeptic tunes an agent that is not installed — no framework or [subagents.*] declaration provides builtin:onto-skeptic …
```

A tune-only entry cannot set `scope`, `repo`, `mode`, `targets`, `version`,
or `digest`. Put `steps` in the `.opencode` block, not under
`[subagents.<name>]`.

One builtin agent can be installed under one name only. Declaring
`builtin:onto-reviewer` as `[subagents.audit]` is fine on its own, but not
alongside `[engines.onto]`, which already installs it.

### `bash_allow_add`

Appends **exact** commands to an agent's shell allowlist. Patterns, shell
composition, environment assignments, credentials, and destructive or
privilege-escalating commands are rejected at load. Protected asks and denies
in the agent's profile still win. Generate candidates with
`homonto permissions suggest`:

```console
$ printf 'go test ./...\nrm -rf build\nmake lint\n' | homonto permissions suggest
# rejected: … "rm -rf build" is a wrapper, destructive, privilege-escalating, shell, or network-fetching command; refusing
# reviewed suggestions — paste under the agent's [subagents.<name>.opencode] block
bash_allow_add = [
  "go test ./...",
  "make lint",
]
```

Like the rest of this block, it applies to builtin agents only. The shipped
coordinator and implementers already allow general shell use, and protected
asks/denies still win over additions, so an addition changes little there; an
agent whose profile denies shell (`bash: false`, every read-only specialist)
cannot gain additions at all.

## Engines — `[engines.<name>]`

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[engines.to]
source = "builtin:to"
scope  = "project"

[engines.no]
source = "builtin:no"
scope  = "project"
```

| Builtin | Installs | Agents that need a model |
|---|---|---|
| `builtin:onto` | 10 skills, 10 commands, workflow plugin | `homonto`, `onto-explorer`, `onto-implementer`, `onto-reviewer`, `onto-skeptic` |
| `builtin:to` | 6 skills, 6 commands, workflow plugin | `homonto`, `to-explorer`, `to-implementer`, `to-reviewer`, `to-skeptic` |
| `builtin:no` | `no` skill, `/no` command | `no-implementer`, `no-reviewer` |

`no` takes one setting, `isolation` (`ask`, `existing` or `worktree`;
default `ask`):

```toml
[engines.no.settings]
isolation = "worktree"
```

Any package can declare settings and named process definitions in its
manifest; `homonto explain settings` lists them with defaults marked
([settings contract](../contracts/settings.md)).

Engines coexist; `homonto` is declared once even when several packages
install it. The full inventory is in the [catalog](catalog.md). A `local:` or
`remote:` package root may be declared too; its `framework.toml` defines its
content. `remote:` requires `digest`.

## Workflows — `[workflows.<name>]`

```toml
[workflows.h-workflow-gh]
source = "builtin:h-workflow-gh"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"
```

`builtin:h-workflow-gh` installs the `homonto` skill and coordinator, five `h-*` skills
and commands, the `h-spike` and `h-review` workers, the workflow plugin, and
the GitHub draft tools. It installs **no** engine. `/h-resolve-issue` needs
one installed engine to implement with; the other `h` commands do not.

Builtin roles are fixed. `builtin:h-workflow-gh` under `[engines]` fails:

```text
parse config: engines.h-workflow-gh source "builtin:h-workflow-gh" is a workflow, not an engine
```

Legacy `[frameworks.*]` tables are rejected; rename them to `[engines.*]` or
`[workflows.*]`.

## Services — `[services.<name>]`

A service package connects a host or ticket service: its operations, the CLI
or transport it uses, the credentials it needs, and any shell rules it adds to
rendered agents ([service contract](../contracts/service.md)).

```toml
# GitHub drafts and publication for a custom workflow, with no h workflow.
[services.host-gh]
source = "builtin:host-gh"
scope  = "project"
```

| Builtin | Service | Runtime provider | Transport |
|---|---|---|---|
| `builtin:host-gh` | `github` | GitHub draft tools | `gh` |
| `builtin:host-gl` | `gitlab` | GitLab draft tools | `glab` |
| `builtin:host-bb` | `bitbucket` | Bitbucket draft tools | `homonto host bitbucket api` |

A workflow names the services it needs and a default package for each. The
`h-workflow-*` packages default to the matching `host-*`, so
`[workflows.h-workflow-gh]` alone works as before. Declaring a service with the
same service id replaces the default. Two installed packages implementing one
service id fail at load.

## Plugins — `[plugins.opencode.<name>]`

```toml
[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"   # npm package → OpenCode's plugin array

[plugins.opencode.experimental]
source  = "@acme/opencode-experiment"
enabled = false                       # declared but not loaded
```

| Field | Required | Notes |
|---|---|---|
| `source` | yes | The npm package or plugin path. Two plugins may not share a source. |
| `enabled` | no | Default `true`. |

OpenCode has no per-plugin configuration on disk, so a `config` table is
rejected. The bundled `permission-observer` plugin is declared like this:

```toml
[plugins.opencode.permission-observer]
source = "permission-observer"
```

It watches shell permission approvals in memory and, after two explicit
approvals of the same exact command, suggests adding it with
`homonto permissions suggest`. It never edits config.

## Settings — `[settings.opencode]`

Arbitrary keys merged into the **global** OpenCode config
(`~/.config/opencode/opencode.jsonc`):

```toml
[settings.opencode]
model       = "anthropic/claude-opus-5-5"
small_model = "anthropic/claude-haiku-5-5"
autoupdate  = false
```

homonto sets the main session model only when you declare it here.

Reserved keys fail at load:

| Key | Use instead |
|---|---|
| `mcp` | Your own `opencode.jsonc` (MCP servers are bring-your-own) |
| `plugin`, `plugins` | `[plugins.opencode.<name>]` |
| `model_variant` | `variant` on a specific agent |

## Terminal settings — `[tui.opencode]`

Projects into the OpenCode **V2** terminal config,
`$XDG_CONFIG_HOME/opencode/cli.json` (default `~/.config/opencode/cli.json`).
There is no project-level terminal file.

```toml
[tui.opencode]
theme  = { name = "gruvbox", mode = "system" }
scroll = { speed = 3, acceleration = false }
tabs   = { mode = "auto" }

[tui.opencode.keybinds]
"app.exit"  = ["ctrl+c", "<leader>q"]
"help.show" = false
```

Supported groups follow the [V2 CLI config](https://opencode.ai/v2/docs/cli/config/):
`theme`, `cursor`, `scroll`, `leader`, `prompt`, `session`, `tabs`, `diffs`,
`attention`, `terminal`, `mini`, `keybinds`, `debug`, `experimental`,
`animations`, `mouse`, `$schema`. Quote keybinding IDs that contain dots.
Unknown fields and invalid values fail before projection.

OpenCode 1 spellings fail at load and name their V2 key: `theme = "x"` →
`theme.name`, `scroll_speed` → `scroll.speed`, `scroll_acceleration` →
`scroll.acceleration`, `leader_timeout` → `leader.timeout`. More in
[OpenCode integration](opencode.md#terminal-settings).

## Integrations — `[integrations.opencode]`

Controls the bundled `homonto-workflow` plugin.

```toml
[integrations.opencode]
workflow_context = true    # V2 read-only context + status panel
```

| Key | Default | Meaning |
|---|---|---|
| `workflow_context` | `true` when `builtin:onto`, `builtin:to`, or `builtin:h-workflow-gh` is declared; else `false` | Links `.opencode/plugins/homonto-workflow-context`: workflow context for model requests and compaction, the **Open workflow status** panel, `homonto_status` / `homonto_handoff` tools, and (with `h`) GitHub draft tools. |

The OpenCode 1 bridge is removed: `workflow_bridge = true`, or an enabled plugin
declared with `source = "homonto-workflow"`, fails at load and names the key.
A leftover `workflow_bridge = false` is ignored; delete it. Opt out of the
plugin entirely:

```toml
[integrations.opencode]
workflow_context = false
```

Details: [OpenCode integration](opencode.md).

## Tooling — `[tooling]`

Declares which optional developer tools the workflow skills should use.

```toml
[tooling]
shell_proxy = "rtk"        # rtk | none
code_intel  = "graphify"   # graphify | okf | none
```

| Key | Values | Default | Effect |
|---|---|---|---|
| `shell_proxy` | `rtk`, `none` | `none` | Workflows route shell commands through rtk, a token-optimizing CLI proxy. The coordinator also gets an `rtk *` allow. |
| `code_intel` | `graphify`, `okf`, `none` | `none` | Open/design phases ground code claims in that index ([okf](https://github.com/UmairBaig8/okf-generator)). |

homonto **never installs, updates, or runs** a provider. Apply writes a
generated `references/tooling.md` into each dispatcher skill naming exactly
what you declared; the skills warn and continue when a declared provider is
missing. `homonto doctor` probes `PATH` and reports a missing provider as a
warning. Unknown values fail:

```text
tooling.code_intel "ctags" is not a known provider (accepted: graphify, okf, none)
```

## Scratch directory — `[tmp]`

One directory, inside the config root, that every writable agent may use
without prompts.

```toml
[tmp]
dir = ".tmp"   # default when the table is present
```

Apply creates the directory, adds an anchored `/.tmp/` line to `.gitignore`,
and writes `references/tmp.md` into the dispatcher skills and the shared
`homonto` skill so agents put handoff drafts, fetched packets, and scratch
output there instead of random `mktemp` paths. Removing the table withdraws
the references. A change keeps its scratch under `<dir>/<engine>/<change>/`;
[cleanup](workflow-cleanup.md) deletes that folder at close, and
`homonto tmp clean` empties scratch that belongs to no active change. `apply`
itself never deletes the directory's contents.

`dir` must be relative, below the config root, and may not overlap `.git`,
`.opencode/`, `homonto/`, the workflow root, or `.homonto/{catalog,remote,cache}`.
Cleanup and `tmp clean` also refuse a `dir` that overlaps `[worktrees] dir` or
contains a `[repos]` checkout. `[tmp]` takes only `dir`:

```text
tmp.path ".tmp" is an unknown key — [tmp] takes only dir
```

## Workflow records — `[workflow]`

```toml
[workflow]
root = "docs"        # default
```

| Field | Default | Notes |
|---|---|---|
| `root` | `docs` | Relative to the config root. Schema 2 also accepts external and absolute paths. |
| `git` | `existing` | Schema 2 only. `existing`: records use their Git owner's normal commit policy. `managed`: a homonto-owned records repository with automatic, path-scoped checkpoints. |

In schema 0/1 the root must stay below the config root (no absolute paths, no
`..`, no escaping symlinks). Schema 2 rules and the managed mode are in
[workspaces](workspaces.md). Changing `root`, `git`, or the schema while
records exist fails closed rather than moving anything.

## Repositories — `[repos]`

Name source repositories with aliases. Paths are relative to the config root
or absolute, and must be the top level of a Git worktree.

```toml
# schema 0/1: the config checkout is implicit; list only siblings
[repos]
api = "../api"
web = "../web"
```

```toml
# schema 2: list every source, including the config checkout if it has code
schema_version = 2

[repos]
app = "."
api = "../api"
```

Two aliases may not point at the same repository (same Git common directory),
even through symlinks or linked worktrees. Paths containing `*`, `?`,
backslashes, or control characters are rejected.

What declaring a repository does:

- **Agent access.** For builtin packages, apply grants the coordinator and
  implementers OpenCode `external_directory` access to declared repositories
  (and their worktree namespaces), after a deny rule for every other external
  path. Read-only specialists can read them. Implementers are denied the
  records root. Custom agents get nothing.
- **Per-repo resources.** Project-scoped skills, commands, and subagents can
  target one repository with `repo = "<alias>"`. Each repository
  gets its own state partition (`.homonto/state.<alias>.json`) and is
  labelled `opencode@<alias>` in `plan` and `status`.
- **Change scope.** `onto new` / `to new` select sources with `--repo <alias>`
  (required in schema 2).

```toml
[repos]
api = "../api"

[skills.handoff]
source = "builtin:handoff"
scope  = "project"
repo   = "api"          # links into ../api/.opencode/skills/
```

These grants are **not a sandbox**: OpenCode matches paths lexically, so a
symlink inside a declared repository can lead outside it, and allowed scripts
run with your privileges. Declare only repositories you trust.

## Worktrees — `[worktrees]`

Schema 2 only. The parent directory for registered per-change checkouts:

```toml
[worktrees]
dir = "../execution"
```

No default: without `dir`, no worktrees are allocated. Apply renders
instructions and per-alias permissions; it creates no worktree. Allocation is
`homonto worktree create` (onto/to) or `homonto workspace task create`
(shared tasks). See [workspaces](workspaces.md).

## Integration checks — `[integration.<name>]`

Schema 2 only. Named checks that run against a task's actual checkouts when
any listed repository changed:

```toml
[integration.api-web-contract]
repos   = ["api", "web"]           # changing any of these selects the check
repo    = "web"                    # which checkout the command runs in
cwd     = "tests/contract"         # path inside that checkout ("." = root)
command = ["npm", "run", "contract"]
```

| Field | Notes |
|---|---|
| `repos` | Unique declared aliases, or `["*"]` for every repository in `[repos]`. `"*"` must be the only entry. |
| `repo` | One of `repos`. |
| `cwd` | Required; relative, no `..`, no symlink escape. |
| `command` | Non-empty argv array (not a shell string). Use `["bash", "-c", "…"]` if you need a shell. |

The command receives `HOMONTO_INTEGRATION_CONTEXT`, the path of a JSON file
with every participating checkout:

```json
{
  "version": 1,
  "task": "api-contract",
  "engine": "no",
  "check": "api-web-contract",
  "repos": {
    "api": {"path": "/work/execution/api/…", "revision": "<commit>", "git_dir": "<git-dir>"},
    "web": {"path": "/work/execution/web/…", "revision": "<commit>", "git_dir": "<git-dir>"}
  }
}
```

A check over every repository selects on any change and pins all of their
checkouts, so the whole set is verified together:

```toml
[integration.everything]
repos   = ["*"]
repo    = "web"
cwd     = "."
command = ["make", "e2e"]
```

Scripts must read those paths, not guess sibling locations. Run checks with
`homonto workspace task check`; see [workspaces](workspaces.md#workspace-tasks).

## Model routing — `[routing]`

A routing policy picks the execution model for agents by where they run,
not by name. Each selector takes one policy per harness, in that harness's
own model spelling. The full contract, including stage agreement and auto
selection, is [model routing](../contracts/routing.md).

```toml
[routing.default.opencode]                  # every agent, coordinator included
model = "anthropic/claude-sonnet-5-5"

[routing.default.claude]
model = "sonnet"

[routing.engines.onto.stages.verify.claude] # agents bound to onto's verify stage
model = "opus"

[routing.engines.no.opencode]               # chosen at plan from what OpenCode reports
mode = "auto"
candidates = ["openai/gpt-6-mini", "anthropic/claude-haiku-5-5"]
needs = ["tools"]
prefer = "cost"
fallback = "anthropic/claude-sonnet-5-5"

[routing.models.opencode."openai/gpt-6-mini"]
cost = 0.6          # your own unit, compared only
latency = 0.8
tags = ["tools"]
```

| Selector | Applies to |
|---|---|
| `routing.default.<harness>` | every agent |
| `routing.workflows.<workflow>.<harness>` | the workflow's agents |
| `routing.engines.<engine>.<harness>` | the engine's agents |
| `routing.engines.<engine>.stages.<stage>.<harness>` | agents bound to that stage |

An explicit `[subagents.<name>.<harness>]` `model` wins over every policy.
Otherwise the most specific selector that every stage an agent serves agrees
on applies. A static policy needs `model`; an auto policy needs `fallback`
and takes `candidates`, `providers` (OpenCode only), `needs`, `max_cost`,
`max_latency` and `prefer` (`listed`, `cost`, `latency`). `homonto explain
routing` shows which selector chose each agent's model.

## Decision models — `[decisions]`

Decision providers answer typed judgments that packages ask for
(`homonto decide`). They are configured apart from execution models;
routing never reads this table. Contract:
[decision calls](../contracts/decisions.md).

```toml
[decisions]
default = "openai"          # provider for points without their own entry
min_confidence = 0.6        # below this an answer is reported uncertain
timeout = "30s"

[decisions.providers.openai]
protocol = "openai-decisions"
model = "gpt-6-luna"
api_key_env = "OPENAI_API_KEY"   # the variable's name; the key never goes in the file

[decisions.providers.jev]
protocol = "systemone"           # Jev / System One-compatible
endpoint = "https://openrouter.ai/api/v1/systemone"   # required: services differ
model = "typesafe/jev-1.14"
api_key_env = "OPENROUTER_API_KEY"

[decisions.points."h-workflow-gh.triage"]
provider = "jev"
min_confidence = 0.8
```

Provider names are lowercase letters, digits and `-`. A point is
`<package>.<point>`.

## Package settings and processes

A package declares the settings and process definitions it accepts in its
manifest. You set them on the package's own table, and homonto checks them
against that schema at load ([settings contract](../contracts/settings.md)):

```toml
[engines.no]
source = "builtin:no"
scope  = "project"

[engines.no.settings]
isolation = "worktree"

[workflows.flow.processes.triage]   # a named definition the package interprets
steps = ["read", "label"]
```

Settings and processes go on `[engines.*]`, `[workflows.*]` and `[services.*]`
only. An unknown key, a wrong type, a value outside the enum, or a missing
required setting fails at load with the exact key. The settings each builtin
package accepts are listed under the [key index](#builtin-package-settings).
`homonto explain settings [package] [--json]` shows the resolved values.

## Claude Code — `[settings.claude]`, `[subagents.<name>.claude]`

`[settings.claude]` keys merge into `~/.claude/settings.json`. Each
Claude-targeted agent needs a model, from `[subagents.<name>.claude]` or a
`[routing.<selector>.claude]` policy, in Claude Code's spelling: an alias
(`sonnet`, `opus`, `haiku`, `inherit`) or a model id. Claude Code agents take
no `variant` or `effort`. See [Claude Code](claude-code.md).

```toml
[settings.claude]
theme = "dark"

[subagents.onto-skeptic.claude]
model = "opus"
```

## Removed and legacy tables

| Table | Status |
|---|---|
| `[frameworks.*]` | Rejected, naming the rename. Use `[engines.*]` / `[workflows.*]`. |
| `[mcps.*]` | Rejected. MCP servers are bring-your-own; configure them in `opencode.jsonc` or `.mcp.json`. |
| `local:` on `[skills.*]` / `[commands.*]` | Rejected. Your own skills and commands are bring-your-own. |
| `[models.<tool>.<tier>]` | Rejected. Model tiers were removed; use per-agent `[subagents.<name>.opencode]`. |
| `[agents.<name>]` | Parses; folds into a copy-mode `[subagents.<name>]`. |
| `[plugins.claude.*]`, `[marketplaces.claude.*]` | Rejected. Claude Code plugins and marketplaces are bring-your-own in its `settings.json`. |

## Diagnostics

Every load error is the same three facts: the full key path, the value
that was refused (when there is one), and what to write instead. Load stops
at the first error, before anything is planned or written.

```text
parse config: engines.no.settings.isolation: "always" is not one of ask, existing, worktree
parse config: routing.default.opencode is an auto policy and needs a fallback model for when discovery fails or nothing qualifies
parse config: frameworks entry "onto" — [frameworks.*] was split by role; declare it as [engines.onto] or [workflows.onto] (docs/guides/configuration.md)
```

Each rejection class below is checked by a test
(`TestDiagnosticsNameKeyValueAndFix` in `internal/config`). A new class adds
a case there.

| Class | Examples |
|---|---|
| Decoding | unknown key or table, newer or negative `schema_version` |
| Removed surfaces | `[mcps]`, `[models]`, `[frameworks]`, `[plugins.claude]`, `[marketplaces.claude]`, `workflow_bridge`, `local:` skills and commands |
| Resources | missing or invalid `scope`, invalid `source`, `remote:` without `digest`, `digest` without `remote:`, unknown `targets` or `harnesses`, a package under the wrong kind table, invalid subagent `mode` |
| Agent models | missing model, `#variant` suffix, OpenCode `effort`, non-positive `steps`, invalid `variant`, non-exact `bash_allow_add`, tuning an agent nothing installs, tune-only entries with declaration fields, Claude Code `variant` |
| Native settings | reserved `settings.opencode` keys, OpenCode plugin `config`, empty plugin `source` |
| Workspace layout | `[tmp]` keys and paths, `workflow.git` and `workflow.root`, missing `[repos]` paths, schema 2-only tables on a legacy config, unknown `[tooling]` values |
| Routing | static without `model`, auto without `fallback`, auto with `model`, unknown `mode` or `prefer`, model ids in the wrong spelling, selectors naming undeclared packages, `providers` on Claude Code |
| Decisions | unknown `protocol`, missing `endpoint`, invalid provider names |
| Package settings | unknown setting, value outside type or enum, settings on a skill or command |

## Key index

<!-- BEGIN GENERATED KEY INDEX (task docs-config) -->

Every key the decoder accepts, generated from the config schema. `<name>`
and similar segments are yours to choose. A key marked *rejected* or
*removed* is decoded only so that load can refuse it and name the fix.

| Key | Type | Notes |
|---|---|---|
| `integration.<name>.repos` | list of strings | schema 2 only; declared aliases, or ["*"] |
| `integration.<name>.repo` | string | schema 2 only; one of repos |
| `integration.<name>.cwd` | string | schema 2 only; relative to that checkout |
| `integration.<name>.command` | list of strings | schema 2 only; argv array |
| `schema_version` | integer | 0, 1 or 2; newer fails at load |
| `harnesses` | list of strings | "opencode", "claude"; default ["opencode"] |
| `workflow.root` | string | default "docs" |
| `workflow.git` | string | existing \| managed; schema 2 only |
| `worktrees.dir` | string | schema 2 only |
| `routing.default.<harness>.mode` | string | static (default) \| auto |
| `routing.default.<harness>.model` | string | static only |
| `routing.default.<harness>.variant` | string | static only; OpenCode only |
| `routing.default.<harness>.candidates` | list of strings | auto only |
| `routing.default.<harness>.providers` | list of strings | auto only; OpenCode only |
| `routing.default.<harness>.needs` | list of strings | auto only; tags from routing.models |
| `routing.default.<harness>.max_cost` | number | auto only |
| `routing.default.<harness>.max_latency` | number | auto only |
| `routing.default.<harness>.prefer` | string | auto only: listed (default) \| cost \| latency |
| `routing.default.<harness>.fallback` | string | auto only; required |
| `routing.workflows.<workflow>.<harness>.*` | policy | the routing.default.<harness> fields; a declared workflow |
| `routing.engines.<engine>.<harness>.*` | policy | the routing.default.<harness> fields; a declared engine |
| `routing.engines.<engine>.stages.<stage>.<harness>.*` | policy | the routing.default.<harness> fields; a stage the engine manifest declares |
| `routing.models.<harness>.<model>.cost` | number | your own unit; compared only |
| `routing.models.<harness>.<model>.latency` | number | your own unit; compared only |
| `routing.models.<harness>.<model>.tags` | list of strings | matched by an auto policy's needs |
| `decisions.default` | string | provider for points without their own entry |
| `decisions.min_confidence` | number | 0 to 1 |
| `decisions.timeout` | string | Go duration; default "30s" |
| `decisions.providers.<provider>.protocol` | string | systemone \| openai-decisions |
| `decisions.providers.<provider>.endpoint` | string | required for systemone |
| `decisions.providers.<provider>.model` | string | the provider's model id |
| `decisions.providers.<provider>.api_key_env` | string | the environment variable holding the credential |
| `decisions.points.<package.point>.provider` | string |  |
| `decisions.points.<package.point>.min_confidence` | number |  |
| `mcps.*` | free-form | removed: MCP servers are bring-your-own (ADR 0085) |
| `workflows.<name>.source` | string |  |
| `workflows.<name>.scope` | string | required: user \| project |
| `workflows.<name>.targets` | list of strings |  |
| `workflows.<name>.repo` | string | rejected: packages project into the config root only |
| `workflows.<name>.digest` | string | required with a remote: source |
| `workflows.<name>.settings.*` | free-form | package-defined: the manifest's [settings] schema |
| `workflows.<name>.processes.<process>.*` | free-form | package-defined: the manifest's [processes] schema |
| `engines.<name>.source` | string |  |
| `engines.<name>.scope` | string | required: user \| project |
| `engines.<name>.targets` | list of strings |  |
| `engines.<name>.repo` | string | rejected: packages project into the config root only |
| `engines.<name>.digest` | string | required with a remote: source |
| `engines.<name>.settings.*` | free-form | package-defined: the manifest's [settings] schema |
| `engines.<name>.processes.<process>.*` | free-form | package-defined: the manifest's [processes] schema |
| `services.<name>.source` | string |  |
| `services.<name>.scope` | string | required: user \| project |
| `services.<name>.targets` | list of strings |  |
| `services.<name>.repo` | string | rejected: packages project into the config root only |
| `services.<name>.digest` | string | required with a remote: source |
| `services.<name>.settings.*` | free-form | package-defined: the manifest's [settings] schema |
| `services.<name>.processes.<process>.*` | free-form | package-defined: the manifest's [processes] schema |
| `skills.<name>.source` | string |  |
| `skills.<name>.scope` | string | required: user \| project |
| `skills.<name>.targets` | list of strings |  |
| `skills.<name>.repo` | string |  |
| `skills.<name>.digest` | string | rejected: skills are builtin: only |
| `skills.<name>.settings.*` | free-form | rejected: only packages take settings |
| `skills.<name>.processes.<process>.*` | free-form | rejected: only packages take processes |
| `commands.<name>.source` | string |  |
| `commands.<name>.scope` | string | required: user \| project |
| `commands.<name>.targets` | list of strings |  |
| `commands.<name>.repo` | string |  |
| `commands.<name>.digest` | string | rejected: commands are builtin: only |
| `commands.<name>.settings.*` | free-form | rejected: only packages take settings |
| `commands.<name>.processes.<process>.*` | free-form | rejected: only packages take processes |
| `subagents.<name>.source` | string |  |
| `subagents.<name>.scope` | string | user \| project; default project |
| `subagents.<name>.targets` | list of strings |  |
| `subagents.<name>.repo` | string |  |
| `subagents.<name>.mode` | string | link (default) \| copy |
| `subagents.<name>.version` | string | informational |
| `subagents.<name>.digest` | string | required with a remote: source |
| `subagents.<name>.<harness>.model` | string | OpenCode provider/model; Claude Code alias or model id |
| `subagents.<name>.<harness>.effort` | string | rejected on both harnesses: OpenCode uses variant, Claude Code takes only a model |
| `subagents.<name>.<harness>.variant` | string | OpenCode only; rejected for Claude Code |
| `subagents.<name>.<harness>.steps` | integer | positive; OpenCode steps, Claude Code maxTurns |
| `subagents.<name>.<harness>.bash_allow_add` | list of strings | exact commands only; builtin agents |
| `repos.<alias>` | string | path to a Git worktree's top level |
| `models.*` | removed | removed: model tiers; declare models per agent in [subagents.<name>.<harness>] |
| `tooling.shell_proxy` | string | rtk \| none; default none |
| `tooling.code_intel` | string | graphify \| okf \| none; default none |
| `tmp.dir` | string | default ".tmp" when [tmp] is present |
| `plugins.claude.*` | removed | removed: Claude Code plugins are bring-your-own in its settings.json |
| `plugins.opencode.<name>.source` | string | an npm package, a plugin path, or permission-observer |
| `plugins.opencode.<name>.enabled` | boolean | default true |
| `plugins.opencode.<name>.config.*` | free-form | rejected: OpenCode has no per-plugin config |
| `settings.<harness>.*` | free-form | free-form, merged into opencode.jsonc or ~/.claude/settings.json; OpenCode reserves mcp, plugin, plugins, model_variant |
| `tui.opencode.*` | free-form | OpenCode V2 cli.json schema; unknown fields fail |
| `integrations.opencode.workflow_bridge` | boolean | removed: true fails at load (ADR 0079); delete a leftover false |
| `integrations.opencode.workflow_context` | boolean | default true with onto, to or a hosting workflow |
| `marketplaces.*` | removed | removed: Claude Code marketplaces are bring-your-own in its settings.json |
| `agents.*` | legacy | legacy: [agents.<name>] (source, version, targets, mode) folds into a copy-mode [subagents.<name>] |

### Builtin package settings

Generated from the shipped manifests. Set them under
`[engines.<name>.settings]`, `[workflows.<name>.settings]` or
`[services.<name>.settings]`. `homonto explain settings <package>` shows
the resolved values.

| Package | Kind | Setting | Type | Default | Values |
|---|---|---|---|---|---|
| `no` | engine | `isolation` | string | `ask` | `ask`, `existing`, `worktree` |

<!-- END GENERATED KEY INDEX -->

## A complete annotated example

```toml
schema_version = 2

[workflow]
root = "docs"

[repos]
app = "."

[skills.handoff]
source = "builtin:handoff"
scope  = "project"

[commands.grill]
source = "builtin:grill"
scope  = "user"

[engines.onto]
source = "builtin:onto"
scope  = "project"

[engines.to]
source = "builtin:to"
scope  = "project"

[workflows.h-workflow-gh]
source = "builtin:h-workflow-gh"
scope  = "project"

[tooling]
shell_proxy = "rtk"

[tmp]
dir = ".tmp"

[plugins.opencode.permission-observer]
source = "permission-observer"

[settings.opencode]
model = "anthropic/claude-opus-5-5"

[tui.opencode]
theme = { name = "gruvbox", mode = "system" }

# Every installed agent needs a model.
[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
variant = "high"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"
```
