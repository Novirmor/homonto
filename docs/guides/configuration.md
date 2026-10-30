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
| [`[mcps.<name>]`](#mcp-servers--mcpsname) | MCP servers |
| [`[skills.<name>]`](#skills--skillsname) | Skills |
| [`[commands.<name>]`](#commands--commandsname) | Slash commands |
| [`[subagents.<name>]`](#subagents--subagentsname) | Agent definitions |
| [`[subagents.<name>.opencode]`](#agent-models--subagentsnameopencode) | Each agent's model, variant, step budget, extra allowed commands |
| [`[engines.<name>]`](#engines--enginesname) | Work-model packages: `onto`, `to`, `no` |
| [`[workflows.<name>]`](#workflows--workflowsname) | User-facing packages: `h` |
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

**Targets.** Resources accept an optional `targets` list. The only valid
value is `"opencode"`; omitting it means every tool. `"claude"` and `"codex"`
fail at load (support removed in v0.13.0).

**Scope.** `"project"` writes into the config root (`.opencode/`,
`opencode.jsonc`). `"user"` writes into your global OpenCode config
(`~/.config/opencode/`) and affects every project.

| Resource | `scope` |
|---|---|
| skills, commands, engines, workflows | required |
| subagents | optional, default `project` |
| MCP servers | optional, default `user` |

**Sources.**

| Source | Resolves from | Valid for |
|---|---|---|
| `builtin:<name>` | The catalog in the binary, materialized to `.homonto/catalog/` | skills, commands, subagents, engines, workflows |
| `local:<name>` | `homonto/skills/<name>/`, `homonto/commands/<name>.md`, `homonto/subagents/<name>.md`, or a package root path | skills, commands, subagents, engines, workflows |
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

## MCP servers — `[mcps.<name>]`

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]

[mcps.github]
command = ["github-mcp-server", "stdio"]
env     = { GITHUB_PERSONAL_ACCESS_TOKEN = "${pass:dev/github-token}" }
scope   = "project"
```

| Field | Required | Notes |
|---|---|---|
| `command` | yes | Non-empty argv array. First element is the executable. |
| `env` | no | String map; values may be secret references. |
| `scope` | no | `user` (default) → global `opencode.jsonc`; `project` → `<config-root>/opencode.jsonc`. |
| `repo` | no | A `[repos]` alias; requires `scope = "project"`; writes that repository's `opencode.jsonc`. |
| `targets` | no | `["opencode"]`. |

Projected as an OpenCode `mcp` entry with `"type": "local"` and
`"enabled": true`. Changing `scope` moves the server on the next apply (pruned
from the old file, written to the new one).

## Skills — `[skills.<name>]`

```toml
[skills.house-style]
source = "local:house-style"   # → homonto/skills/house-style/SKILL.md
scope  = "project"

[skills.handoff]
source = "builtin:handoff"     # a catalog skill
scope  = "user"                # available in every project
```

| Field | Required | Notes |
|---|---|---|
| `source` | yes | `builtin:<name>` or `local:<name>`. |
| `scope` | yes | `user` → `~/.config/opencode/skills/`; `project` → `.opencode/skills/`. |
| `repo` | no | A `[repos]` alias; requires `scope = "project"`. |
| `targets` | no | `["opencode"]`. |

Skills are **symlinked**, so editing the source is live immediately. homonto
never creates `homonto/skills/<name>/` for you — write the `SKILL.md` before
applying. Changing `scope` relocates the link.

Builtin skills you can declare on their own: `handoff`, `grilling`. Package
skills (`onto-*`, `to-*`, `h-*`, `homonto`, `no`) come with their package;
don't declare them individually.

## Commands — `[commands.<name>]`

```toml
[commands.grill]
source = "builtin:grill"     # /grill
scope  = "project"

[commands.release-notes]
source = "local:release-notes"   # → homonto/commands/release-notes.md
scope  = "user"
```

Same fields as skills. A command lands at `.opencode/command/<name>.md` (or
the user-scope equivalent). Packages bring their own commands (`/onto`,
`/to`, `/h-*`, `/no`, …); don't redeclare them. Builtin standalone commands:
`grill`, `handoff`.

## Subagents — `[subagents.<name>]`

```toml
# Your own agent file, projected verbatim (put model: in its frontmatter).
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
| `targets` | no | `["opencode"]`. |

Agent file format, permission rendering, and link vs. copy:
[subagents](subagents.md). Legacy `[agents.<name>]` still parses and becomes a
copy-mode subagent.

## Agent models — `[subagents.<name>.opencode]`

**Every builtin agent needs a model** — whether a package installs it or you
declare it with a `builtin:` source. There are no defaults, tiers, or
inheritance. `local:` and `remote:` agents are projected verbatim and may
**not** have this block; set `model:` in their own frontmatter instead:

```text
parse config: subagents.security-review declares a model override, but its source "local:security-review" is not builtin: — local:/remote: agents are projected verbatim and never rendered, so the override could never apply
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

Engines coexist; `homonto` is declared once even when several packages
install it. The full inventory is in the [catalog](catalog.md). A `local:` or
`remote:` package root may be declared too; its `framework.toml` defines its
content. `remote:` requires `digest`.

## Workflows — `[workflows.<name>]`

```toml
[workflows.h]
source = "builtin:h"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"
```

`builtin:h` installs the `homonto` skill and coordinator, five `h-*` skills
and commands, the `h-spike` and `h-review` workers, the workflow plugin, and
the GitHub draft tools. It installs **no** engine. `/h-resolve-issue` needs
one installed engine to implement with; the other `h` commands do not.

Builtin roles are fixed. `builtin:h` under `[engines]` fails:

```text
parse config: engines.h source "builtin:h" is a workflow, not an engine
```

Legacy `[frameworks.*]` tables are rejected; rename them to `[engines.*]` or
`[workflows.*]`.

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
| `mcp` | `[mcps.<name>]` |
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
| `workflow_context` | `true` when `builtin:onto`, `builtin:to`, or `builtin:h` is declared; else `false` | Links `.opencode/plugins/homonto-workflow-context`: workflow context for model requests and compaction, the **Open workflow status** panel, `homonto_status` / `homonto_handoff` tools, and (with `h`) GitHub draft tools. |

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
the references. **homonto never deletes the directory's contents.**

`dir` must be relative, below the config root, and may not overlap `.git`,
`.opencode/`, `homonto/`, the workflow root, or `.homonto/{catalog,remote,cache}`.
`[tmp]` takes only `dir`:

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
- **Per-repo resources.** Project-scoped skills, commands, subagents, and MCP
  servers can target one repository with `repo = "<alias>"`. Each repository
  gets its own state partition (`.homonto/state.<alias>.json`) and is
  labelled `opencode@<alias>` in `plan` and `status`.
- **Change scope.** `onto new` / `to new` select sources with `--repo <alias>`
  (required in schema 2).

```toml
[repos]
api = "../api"

[skills.api-conventions]
source = "local:api-conventions"
scope  = "project"
repo   = "api"          # links into ../api/.opencode/skills/

[mcps.api-db]
command = ["pg-mcp", "--dsn", "${pass:api/dev-dsn}"]
scope   = "project"
repo    = "api"          # writes ../api/opencode.jsonc
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

## Removed and legacy tables

| Table | Status |
|---|---|
| `[frameworks.*]` | Rejected. Use `[engines.*]` / `[workflows.*]`. |
| `[models.<tool>.<tier>]` | Rejected. Model tiers were removed; use per-agent `[subagents.<name>.opencode]`. |
| `[agents.<name>]` | Parses; folds into a copy-mode `[subagents.<name>]`. |
| `[settings.claude]`, `[plugins.claude.*]`, `[marketplaces.claude.*]`, `[subagents.<name>.claude]` | Rejected since v0.13.0. |

## A complete annotated example

```toml
schema_version = 2

[workflow]
root = "docs"

[repos]
app = "."

[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]
scope   = "project"

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env     = { BRAVE_API_KEY = "${pass:ai/brave}" }

[skills.house-style]
source = "local:house-style"
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

[workflows.h]
source = "builtin:h"
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
