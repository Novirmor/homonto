# Configuration reference — `homonto.toml`

One file, parsed into a tool-agnostic desired state. All sections are
optional; an empty config is valid and projects nothing. `homonto plan`,
`apply`, `status`, and `doctor` accept `--config <path>` (default
`homonto.toml`). OpenCode is the only adapter; Claude Code and codex support
was removed in v0.13.0 (configs naming them fail at load naming the key).

Quick map of every table:

| Table | Declares | Reference |
|---|---|---|
| `schema_version` | The config format version (top-level key, not a table) | [Schema version](#schema-version) |
| `[workflow]` | Shared onto/to workflow artifact root | [Workflow root](#workflow-root--workflow) |
| `[mcps.<name>]` | MCP servers | [MCP servers](#mcp-servers--mcpsname) |
| `[skills.<name>]` | Skills (symlinked) | [Skills](#skills--skillsname) |
| `[commands.<name>]` | Slash commands | [Commands](#commands--commandsname) |
| `[subagents.<name>]` | Agent definitions | [Subagents](#subagents--subagentsname) |
| `[subagents.<name>.opencode]` | Per-subagent model override (required for every declared subagent) | [Subagent models](#subagent-models--subagentsnameopencode) |
| `[frameworks.<name>]` | Bundled framework installs | [Frameworks](#frameworks--frameworksname) |
| `[plugins.opencode.<name>]` | OpenCode plugins | [Plugins](#plugins--pluginsopencodename) |
| `[settings.opencode]` | OpenCode settings | [Settings](#settings--settingsopencode) |
| `[tui.opencode]` | OpenCode TUI settings | [TUI](#tui--tuiopencode) |
| `[agents.<name>]` | Legacy — folds into `[subagents.<name>]` | [Legacy agents](#legacy-agents--agentsname) |

## Schema version

`schema_version` is an optional top-level key (not a table) naming the
`homonto.toml` format version. The current version is **1**.

```toml
schema_version = 1
```

Omitting it, or setting `0`, means a legacy pre-versioning config and is
treated as the current version — so existing files keep working untouched. A
value **greater** than the binary supports is **rejected fail-closed at
load**, rather than partially applied: an older `homonto` never silently
mis-projects a config written for a newer one. Upgrade the binary, or lower
the declared version.

## Common concepts

**Targets.** Most resources take an optional `targets` list selecting which
tools they project into. OpenCode is the only adapter, so the only valid
value is `"opencode"`, and omitting the list means every tool. A typo like
`targets = ["opencod"]` fails at load, not silently; a target naming
`"claude"` or `"codex"` fails at load citing the v0.13.0 removal.

**Sources.** Skills, commands, subagents, and frameworks resolve their
content through a `source` string:

| Source | Resolves from | Available for |
|---|---|---|
| `builtin:<name>` | the catalog embedded in the binary, materialized under `.homonto/catalog/` | skills, commands, subagents, frameworks |
| `local:<name>` | your own content next to `homonto.toml` (`homonto/skills/<name>/`, `homonto/subagents/<name>.md`, a framework root) | skills, commands, subagents, frameworks |
| `remote:<url>` | a fetched, verified, pinned archive; requires `digest` (see [remote source trust](remote-source-trust.md)) | subagents, frameworks |

**Validation is fail-fast.** homonto rejects at load time and names the
offender: an MCP with no command, an unknown target, a declared subagent
without a `[subagents.<name>.opencode]` model block, a settings key that
collides with a structure homonto manages (`settings.opencode.mcp`,
`settings.opencode.plugin`), a skill without a `scope`, a `remote:` source
without a `digest`, a legacy `[models.<tool>.<tier>]` block (tiers were
removed), and names that would corrupt a JSON file (empty, or index-like such
as `"0"`/`"-1"`).

**Config root and bootstrap.** The directory containing `homonto.toml` is the
configuration root. Run `homonto init [dir]` to scaffold that configuration;
it never runs `git init` and never installs a framework by itself. Add a
`[frameworks.onto]` or `[frameworks.to]` table, inspect `homonto plan`, then
run `homonto apply` to install the selected workflow. A Git worktree is needed
only when a later workflow gate requires Git evidence or integration.

## Workflow root — `[workflow]`

`[workflow]` selects the one repository-relative root for workflow artifacts.
It is shared by the mutually exclusive `onto` and `to` frameworks. Omit it to
preserve the default `docs` layout.

```toml
[workflow]
root = "workflow-records"
```

The root must remain below the directory containing `homonto.toml`; absolute
paths and `..` escapes fail at load. In documentation, `<workflow-root>` means
this configured directory: onto uses `<workflow-root>/changes`,
`specs`, `adr`, and `guides`; to uses `<workflow-root>/tasks`. Changing `root`
while workflow state exists fails closed. Homonto never moves workspaces,
archives, locks, receipts, or recovery packs automatically.

## MCP servers — `[mcps.<name>]`

MCP declarations exist so a server's command, scope, and secret references are
reviewable and reproducible rather than hand-edited into `opencode.jsonc`. They
are optional: no MCP declarations means no MCP servers are projected.

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]   # required, non-empty
env     = { API_KEY = "${pass:ai/key}" }    # optional; values may be secret references
targets = ["opencode"]                      # optional; default: every tool
scope   = "project"                         # optional; user (default) | project
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `command` | array of strings | **yes** | first element is the executable, the rest are args |
| `env` | table of strings | no | values may hold `${pass:…}` / `${ENV_VAR}` references — never plaintext secrets |
| `targets` | array | no | default: every tool; `"opencode"` is the only valid value |
| `scope` | string | no | `user` (default) → global tool config; `project` → the project-level config the tool merges over it |
| `repo` | string | no | a declared `[repos]` name; requires `scope = "project"` and writes only that repo's project config |

Projection at user scope: OpenCode global `opencode.jsonc` `mcp`
(`type: local`). At project scope: OpenCode `<repo>/opencode.jsonc` `mcp` —
the server runs only in that repository's sessions instead of everywhere.
Switching an applied server's scope migrates it on the next `apply` (pruned
from the old file, written to the new one).

## Skills — `[skills.<name>]`

```toml
[skills.graphify]
source = "local:graphify"    # local:<name> → homonto/skills/<name>/, or builtin:<name>
scope  = "project"           # REQUIRED: user | project (no default)
targets = ["opencode"]       # optional; default: every tool
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | `local:<name>` or `builtin:<name>` |
| `scope` | string | **yes** | `user` → `~/.config/opencode/skills/`; `project` → `<repo>/.opencode/skills/` |
| `targets` | array | no | default: every tool |
| `repo` | string | no | a declared `[repos]` name; requires `scope = "project"` and links only into that repo |

Skills are **symlinked**, not copied, so editing `homonto/skills/<name>/` is
instantly live in every tool. Switching a skill's `scope` relocates the link
cleanly: `plan` shows the move, and `apply` removes the old link as it
creates the new one. `scope` affects skills, commands, subagents, and
[MCP servers](#mcp-servers--mcpsname) directly; explicit `[settings.<tool>]`
keys always project into the global tool files. Subagent models are declared
per agent — see
[subagent models](#subagent-models--subagentsnametool).

## Commands — `[commands.<name>]`

Slash commands, materialized as single files under
`.homonto/catalog/commands/` and linked into OpenCode's command directory,
`.opencode/command/<name>.md` (or the user-scope equivalent).

```toml
[commands.grill]
source = "builtin:grill"     # builtin:<name> | local:<name>
scope  = "project"           # user | project
repo   = "service-a"         # optional declared [repos] name
```

Frameworks declare their own commands too (onto ships `/onto`, `/onto-open`,
…); those project the same way without being declared here.

`repo` has the same rule as skills: it is optional, requires
`scope = "project"`, and links the command only into the named declared
repository's `.opencode/command/` directory.

## Subagents — `[subagents.<name>]`

Agent definitions (markdown with frontmatter), projected into each tool's
agent directory. Fully declarative: reconciled by
`plan`/`apply`/`status`/`doctor` like every other resource. There is no
imperative "agents" command group.

```toml
[subagents.review]
source = "builtin:onto-reviewer"   # builtin:<name> | local:<name> | remote:<url>
scope  = "project"                 # user | project (default: project)
mode   = "copy"                    # link (symlink, default) | copy (managed file)
targets = ["opencode"]             # optional; default: every tool
repo = "service-a"                 # optional declared [repos] name

[subagents.reviewer]               # a remote, pinned agent
source = "remote:https://example.com/reviewer.tar.gz"
digest = "sha256:<64 hex>"         # REQUIRED for remote:; verified before any write
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | `builtin:` ships the onto primary and specialists, or the parallel to primary and specialists; `local:` → `homonto/subagents/<name>.md`; `remote:` → pinned archive |
| `scope` | string | no | `user` \| `project` (default `project`) |
| `mode` | string | no | `link` (default) or `copy` — see [subagents](subagents.md) |
| `targets` | array | no | default: every tool |
| `repo` | string | no | a declared `[repos]` name; requires `scope = "project"` and writes only that repo's agent directory |
| `digest` | string | remote only | `sha256:<64 hex>` content pin, required for `remote:` |
| `version` | string | no | informational until pinning is wired |

Where they land, link vs. copy semantics, and the tool-neutral `homonto:`
frontmatter block: [subagents](subagents.md). The remote pipeline:
[remote source trust](remote-source-trust.md).

## Frameworks — `[frameworks.<name>]`

A framework is a bundled set of skills, commands, and subagents that install
together, with dependency expansion. The builtin catalog ships exactly the
two homonto-native frameworks, `onto` and `to`, and they are **mutually
exclusive**: declaring both fails at load (one workflow per repository).
Both frameworks install the shared `homonto` knowledge skill and their own
selectable primary workflow agent.
Beyond `builtin:`, a framework source may be `local:<path>` (a framework root
in your repo) or `remote:<url>` with a required `digest = "sha256:…"` pin.
Third-party workflow stacks are not bundled.

```toml
[frameworks.onto]        # or [frameworks.to] — never both
source = "builtin:onto"
scope  = "project"
```

## Repos — `[repos]`

Declares the other repositories this config operates across
([ADR 0024](../adr/0024-multi-repo-designated-state-cross-repo-effect.md)):
`name = "<path>"`, paths relative to the config file (absolute honored). The
config repository itself is implicit and never listed. Every entry must exist
and be a git worktree; two names may not resolve to one repository — all
checked at load, fail-closed.

```toml
[repos]
service-a = "../service-a"
service-b = "../libs/service-b"
```

The config repository remains the designated state home: `.homonto/`,
onto's `<workflow-root>/changes/`, and to's `<workflow-root>/tasks/` stay there. `homonto plan`
names every declared repo and `homonto doctor` reports each one's health.

### Workflow access to declared repositories

`[repos]` is also the trust boundary for the bundled workflow teams. When an
`onto` or `to` framework is installed, `homonto apply` renders each resolved
repository path as an OpenCode `external_directory` allow rule for that
framework's builtin writable primary and implementer. They can read, edit, and
run their existing approved commands there without a per-directory prompt.
Homonto emits a deny rule before the declared paths, so a global OpenCode allow
does not broaden these roles. Read-only specialists and custom agents receive no
rule. Paths containing `*` or `?` are rejected because OpenCode treats them as
permission wildcards.

OpenCode authorizes external paths lexically rather than resolving symlinks.
Treat a declared repository and its links as trusted; a symlink within it can
lead outside the declared root. `[repos]` constrains agent workspace selection,
not filesystem containment against a hostile repository.

Changing a declared path and re-running `homonto apply` re-renders the affected
agent files. The config repository is already the active workspace, so it is
implicit and cannot appear in `[repos]`.

For a project-scoped skill, command, subagent, or MCP, `repo = "<name>"`
projects that one resource into the named declared repo. A repo-tagged skill,
command, or subagent links under that repo's `.opencode/`; a repo-tagged MCP
writes that repo's `opencode.jsonc`. The config repo receives untagged
project-scoped resources. User-scoped resources, settings, TUI configuration,
plugins, and frameworks cannot target another repo. Each declared repo has a
separate `.homonto/state.<name>.json` partition in the config repo, so prune,
adoption, and drift remain isolated; `status` labels findings as
`opencode@<name>`.

Framework-declared commands and subagents project exactly like top-level
ones. Do **not** also declare a framework's subagent in a `[subagents.*]`
table; the names collide. `homonto update` re-materializes installed
frameworks at the running binary's version.

## Subagent models — `[subagents.<name>.opencode]`

Every declared subagent must declare a `[subagents.<name>.opencode]` block
with a non-empty `model`. There are no tiers, no roles, no defaults
inherited from a shared route — model selection is explicit per agent. A
declared subagent that lacks the block (or supplies one with an empty
`model`) fails at load naming the offender.

```toml
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope  = "project"

[subagents.onto-reviewer.opencode]
model   = "anthropic/claude-opus-4-8"
variant = "thinking"      # optional
```

| Field | Required | Notes |
|---|---|---|
| `model` | **yes** | the tool's model identifier (`provider/model`) |
| `variant` | no | which variant of the model |

Each value is validated against what OpenCode can actually express, so a
setting the tool would silently ignore becomes a load error naming the
offender:

```
parse config: subagents.onto-reviewer.opencode sets effort "high", but OpenCode has no effort setting — use variant, or drop it
```

### Declared subagent must declare its model

A subagent that supplies no `[subagents.<name>.opencode]` block (or supplies
one with an empty `model`) fails at load:

```
parse config: subagents.onto-reviewer.opencode model is required
```

### Framework agents tune in place

A framework's subagents may not be re-declared explicitly (that collision is an
error), so without a tune-only form there would be no way to supply a model for
a framework-installed agent. A block with **no `source`** therefore
reads as *tune this agent*, not *declare it*. It is required for every expanded
agent, and is the only way a framework agent gets a model:

```toml
[frameworks.onto]
source = "builtin:onto"
scope  = "project"

# Required: onto framework expands onto-skeptic.
[subagents.onto-skeptic.opencode]
model   = "anthropic/claude-opus-4-8"
variant = "thinking"      # optional tune on top of the model
```

For a subagent you declare yourself, add the block under your own
`[subagents.<name>]` entry the same way.

### Legacy `[models.<tool>.<tier>]` blocks are rejected

Model tiers (`architectural`, `coding`, `review`, `trivial`) and the role
frontmatter that mapped to them were removed. A config edited for the old
system fails at load naming the offending table:

```
parse config: models.opencode.architectural is an unknown table — model tiers were removed; declare per-agent models via [subagents.<name>.opencode]
```

### The main session model is operator-controlled

homonto no longer derives a default `model` (or `small_model`) from any route.
Each tool uses its own default unless the operator pins one explicitly via
`[settings.<tool>].model` (see [Settings](#settings--settingstool)).

## Tooling — `[tooling]`

Which optional developer tools the shipped workflow frameworks (`onto`, `to`)
ground against. Both keys are optional and both default to `none`.

```toml
[tooling]
shell_proxy = "rtk"       # "rtk" | "none"
code_intel  = "graphify"  # "graphify" | "okf" | "none"
```

| Key | Accepted | Meaning |
|---|---|---|
| `shell_proxy` | `rtk`, `none` | Routes workflow shell operations through a token-optimizing proxy. Purely a cost optimization. |
| `code_intel` | `graphify`, `okf`, `none` | The code-intelligence provider the open and design phases ground codebase claims in. |

`okf` is [okf-generator](https://github.com/UmairBaig8/okf-generator).

**Both default to `none`.** A config with no `[tooling]` table gets a
preflight that names no tool at all, and grounding falls back to direct file
reading. This is deliberate: before this table existed the frameworks named
`rtk` and `graphify` in their shipped prose, so every user was told about two
tools they might never run.

**homonto never installs, updates, version-checks, or executes a provider.**
Declaring one only changes the rendered instructions. Install it yourself.

**How it reaches the skills.** `homonto apply` writes a generated
`references/tooling.md` into each framework's dispatcher skill describing
exactly the declared pair. The shipped `SKILL.md` files name no provider and
defer to that file, so a provider you did not declare is never mentioned. The
generated file is overwritten on every apply — do not hand-edit it.

Unknown keys and unknown provider names fail at load, naming the offender:

```
tooling.code_intel "ctags" is not a known provider (accepted: graphify, okf, none)
```

`homonto doctor` reports a declared-but-undetected provider as a warning. It
probes `PATH` and index/bundle directories only; it never runs the provider.

## Plugins — `[plugins.opencode.<name>]`

```toml
[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota" # npm package name → the `plugin` array entry
# enabled = false                  # optional; omit → enabled
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | npm package name; two plugins sharing a source fail at load |
| `enabled` | bool | no | omit → enabled |

`[plugins.claude.<name>]` was removed with the Claude Code adapter in
v0.13.0; a config naming it fails at load naming the key.

## Settings — `[settings.opencode]`

Arbitrary keys merged surgically into OpenCode's settings file
(`opencode.jsonc`):

```toml
[settings.opencode]
model = "anthropic/claude-opus-4-8"
```

OpenCode has no global model-variant setting. Configure a variant on a
specific agent with `[subagents.<name>.opencode]`; `model_variant` and a
`#variant` suffix on `model` are rejected.

`bash_allow_add` in a `[subagents.<name>.opencode]` block appends exact
commands to the agent's allowlist — the reviewed output of `homonto
permissions suggest` (ADR 0029). Exact commands only; entries with pattern
metacharacters, shell composition, environment assignments, or destructive
content fail at load.

Bundled plugins (`permission-observer`) are owned catalog content: declaring
`[plugins.opencode.permission-observer]` with `source = "permission-observer"`
projects the materialized plugin path. homonto never executes it; it observes
explicit Bash approvals in memory and suggests allowlist additions exactly
once per candidate.

Keys that collide with structures homonto manages fail at load:
`settings.opencode.mcp`, `settings.opencode.plugin`. `[settings.claude]` was
removed with the Claude Code adapter in v0.13.0; a config naming it fails at
load naming the key.

## TUI — `[tui.opencode]`

OpenCode keeps TUI settings in a separate file
(`~/.config/opencode/tui.json`):

```toml
[tui.opencode]
theme = "gruvbox"
scroll_speed = 3
```

## Marketplaces — `[marketplaces.claude.<name>]`

Removed in v0.13.0: marketplaces were a Claude-only feature and went with
the adapter. A config declaring `[marketplaces.claude.<name>]` fails at load
naming the key; delete the block.

## Legacy agents — `[agents.<name>]`

The legacy `[agents.<name>]` table still parses but folds into a copy-mode
`[subagents.<name>]` at load. Use `[subagents.<name>]` in new configs.

## A complete example

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env = { BRAVE_API_KEY = "${pass:ai/brave}" }

[skills.graphify]
source = "local:graphify"
scope = "project"

[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.review]
source = "builtin:onto-reviewer"
scope = "project"
mode = "copy"

[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"

[settings.opencode]
model = "anthropic/claude-opus-4-8"

[tui.opencode]
theme = "gruvbox"

# Required: every framework-expanded subagent needs a model.
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5"

[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"

# review is explicitly declared above, so it carries its own model block too.
[subagents.review.opencode]
model = "anthropic/claude-opus-4-8"
```
