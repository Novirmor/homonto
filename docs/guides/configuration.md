# Configuration reference — `homonto.toml`

One file, parsed into a tool-agnostic desired state. All sections are
optional; an empty config is valid and projects nothing. `homonto plan`,
`apply`, `status`, `doctor`, and `import` accept `--config <path>` (default
`homonto.toml`).

Quick map of every table:

| Table | Declares | Reference |
|---|---|---|
| `schema_version` | The config format version (top-level key, not a table) | [Schema version](#schema-version) |
| `[mcps.<name>]` | MCP servers | [MCP servers](#mcp-servers--mcpsname) |
| `[skills.<name>]` | Skills (symlinked) | [Skills](#skills--skillsname) |
| `[commands.<name>]` | Slash commands | [Commands](#commands--commandsname) |
| `[subagents.<name>]` | Agent definitions | [Subagents](#subagents--subagentsname) |
| `[subagents.<name>.<tool>]` | Per-tool model overrides (required for every declared subagent) | [Subagent models](#subagent-models--subagentsnametool) |
| `[frameworks.<name>]` | Bundled framework installs | [Frameworks](#frameworks--frameworksname) |
| `[plugins.<tool>.<name>]` | Per-tool plugins | [Plugins](#plugins--pluginstoolname) |
| `[settings.<tool>]` | Per-tool settings | [Settings](#settings--settingstool) |
| `[tui.opencode]` | OpenCode TUI settings | [TUI](#tui--tuiopencode) |
| `[marketplaces.claude.<name>]` | Claude plugin marketplaces | [Marketplaces](#marketplaces--marketplacesclaudename) |
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
tools they project into. Valid values: `"claude"`, `"opencode"`, and (for
MCPs only, as an opt-in pilot) `"codex"`. Omitting the list means both
`claude` and `opencode`. A typo like `targets = ["claud"]` fails at load, not
silently.

**Sources.** Skills, commands, subagents, and frameworks resolve their
content through a `source` string:

| Source | Resolves from | Available for |
|---|---|---|
| `builtin:<name>` | the catalog embedded in the binary, materialized under `.homonto/catalog/` | skills, commands, subagents, frameworks |
| `local:<name>` | your own content next to `homonto.toml` (`homonto/skills/<name>/`, `homonto/subagents/<name>.md`, a framework root) | skills, commands, subagents, frameworks |
| `remote:<url>` | a fetched, verified, pinned archive; requires `digest` (see [remote source trust](remote-source-trust.md)) | subagents, frameworks |

**Validation is fail-fast.** homonto rejects at load time and names the
offender: an MCP with no command, an unknown target, a declared subagent
without a `[subagents.<name>.<tool>]` model block, a settings key that collides
with a structure homonto manages (`settings.claude.enabledPlugins`,
`settings.opencode.mcp`, `settings.opencode.plugin`), a skill without a
`scope`, a `remote:` source without a `digest`, a legacy `[models.<tool>.<tier>]`
block (tiers were removed), and names that would corrupt a JSON file (empty, or
index-like such as `"0"`/`"-1"`).

## MCP servers — `[mcps.<name>]`

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]   # required, non-empty
env     = { API_KEY = "${pass:ai/key}" }    # optional; values may be secret references
targets = ["claude", "opencode", "codex"]   # optional; default: claude + opencode
scope   = "project"                         # optional; user (default) | project
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `command` | array of strings | **yes** | first element is the executable, the rest are args |
| `env` | table of strings | no | values may hold `${pass:…}` / `${ENV_VAR}` references — never plaintext secrets |
| `targets` | array | no | default `["claude", "opencode"]`; add `"codex"` to opt into the Codex pilot |
| `scope` | string | no | `user` (default) → global tool config; `project` → the project-level config the tool merges over it |

Projection at user scope: Claude Code `~/.claude.json` `mcpServers`
(`type: stdio`), OpenCode global `opencode.jsonc` `mcp` (`type: local`),
Codex `~/.codex/config.toml` `[mcp_servers.<name>]` (opt-in only). At
project scope: Claude Code `<repo>/.mcp.json` `mcpServers`, OpenCode
`<repo>/opencode.jsonc` `mcp` — the server runs only in that repository's
sessions instead of everywhere. Codex is user-scope only; a project-scoped
server targeting codex fails at load. Switching an applied server's scope
migrates it on the next `apply` (pruned from the old file, written to the
new one).

## Skills — `[skills.<name>]`

```toml
[skills.graphify]
source = "local:graphify"    # local:<name> → homonto/skills/<name>/, or builtin:<name>
scope  = "project"           # REQUIRED: user | project (no default)
targets = ["claude"]         # optional; default both
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | `local:<name>` or `builtin:<name>` |
| `scope` | string | **yes** | `user` → `~/.claude/skills/`, `~/.config/opencode/skills/`; `project` → `<repo>/.claude/skills/`, `<repo>/.opencode/skills/` |
| `targets` | array | no | default both tools |

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
`.homonto/catalog/commands/` and linked into each tool's command directory:
Claude Code `.claude/commands/<name>.md`, OpenCode
`.opencode/command/<name>.md` (or the user-scope equivalents).

```toml
[commands.grill]
source = "builtin:grill"     # builtin:<name> | local:<name>
scope  = "project"           # user | project
```

Frameworks declare their own commands too (onto ships `/onto`, `/onto-open`,
…); those project the same way without being declared here.

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
targets = ["claude", "opencode"]   # optional; default both

[subagents.reviewer]               # a remote, pinned agent
source = "remote:https://example.com/reviewer.tar.gz"
digest = "sha256:<64 hex>"         # REQUIRED for remote:; verified before any write
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | `builtin:` ships `onto-reviewer`, `onto-explorer`, `onto-implementer`, `onto-skeptic` (and the `to-*` twins); `local:` → `homonto/subagents/<name>.md`; `remote:` → pinned archive |
| `scope` | string | no | `user` \| `project` (default `project`) |
| `mode` | string | no | `link` (default) or `copy` — see [subagents](subagents.md) |
| `targets` | array | no | default both; `codex` has no effect (the pilot is MCP-only) |
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
Beyond `builtin:`, a framework source may be `local:<path>` (a framework root
in your repo) or `remote:<url>` with a required `digest = "sha256:…"` pin.
Third-party workflow stacks are not bundled.

```toml
[frameworks.onto]        # or [frameworks.to] — never both
source = "builtin:onto"
scope  = "project"
```

Framework-declared commands and subagents project exactly like top-level
ones. Do **not** also declare a framework's subagent in a `[subagents.*]`
table; the names collide. `homonto update` re-materializes installed
frameworks at the running binary's version.

## Subagent models — `[subagents.<name>.<tool>]`

Every declared subagent **and every tool it targets** must declare a
`[subagents.<name>.<tool>]` block with a non-empty `model`. There are no tiers,
no roles, no defaults inherited from a shared route — model selection is
explicit per agent per tool. A declared subagent that lacks a model for an
enabled tool fails at load naming the offender.

```toml
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope  = "project"

[subagents.onto-reviewer.claude]
model   = "opus"
effort  = "high"          # optional

[subagents.onto-reviewer.opencode]
model   = "anthropic/claude-opus-4-8"
variant = "thinking"      # optional
```

| Field | Required | Notes |
|---|---|---|
| `model` | **yes** | the tool's model identifier |
| `effort` | no | how hard to think |
| `variant` | no | which variant of the model |

### The two tools spell these differently

The fields are declared neutrally and rendered per tool because each tool
accepts only its own half:

| | Claude Code | OpenCode |
|---|---|---|
| `model` | an alias (`opus`, `sonnet`, `haiku`, `fable`, `opusplan`) or a full id (`claude-opus-4-8`) | `provider/model` |
| `variant` | **has no field** — rendered *into* the model string as `opus[1m]`, and only an **alias** can take one; `1m` is the only documented variant | a **first-class `variant:` field** taking any variant your provider defines |
| `effort` | a real frontmatter field: `low`, `medium`, `high`, `xhigh`, `max` | **no such concept** — declaring it is a config error |

Each value is validated against the tool that will receive it, so a setting
the tool would silently ignore becomes a load error naming the offender:

```
parse config: subagents.onto-reviewer.claude effort "normal" is not a Claude effort level (low, medium, high, xhigh, max)
parse config: subagents.onto-reviewer.claude variant "1m" needs a model alias (…) — Claude takes no variant on the full model id "claude-opus-4-8"
parse config: subagents.onto-reviewer.opencode sets effort "high", but OpenCode has no effort setting — use variant, or drop it
```

### Declared subagent must declare its model

A subagent that targets a tool but supplies no `[subagents.<name>.<tool>]`
block (or supplies one with an empty `model`) fails at load:

```
parse config: subagents.onto-reviewer.opencode model is required
```

Tuning **one** tool does not require the other: a subagent targeting claude
only needs a claude block; an opencode block would be a tune-only entry that
can carry an effort or variant override (see [subagents](subagents.md)).

### Framework agents tune in place

A framework's subagents may not be re-declared explicitly (that collision is an
error), so without a tune-only form there would be no way to supply a model for
a framework-installed agent. A per-tool block with **no `source`** therefore
reads as *tune this agent*, not *declare it*. It is required for every expanded
agent × tool, and is the only way a framework agent gets a model:

```toml
[frameworks.onto]
source = "builtin:onto"
scope  = "project"

# Required: onto framework expands onto-skeptic for both tools.
[subagents.onto-skeptic.claude]
model   = "opus"
effort  = "max"           # optional tune on top of the model
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
```

For a subagent you declare yourself, add the block under your own
`[subagents.<name>]` entry the same way.

### Legacy `[models.<tool>.<tier>]` blocks are rejected

Model tiers (`architectural`, `coding`, `review`, `trivial`) and the role
frontmatter that mapped to them were removed. A config edited for the old
system fails at load naming the offending table:

```
parse config: models.claude.architectural is an unknown table — model tiers were removed; declare per-agent models via [subagents.<name>.claude]
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

## Plugins — `[plugins.<tool>.<name>]`

```toml
[plugins.claude.claude-hud]
source = "claude-hud@official"     # name@marketplace → enabledPlugins key
# enabled = false                  # optional; omit → enabled
# config = { compact = true }      # optional; claude only → pluginConfigs.<source>.options

[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota" # npm package name → the `plugin` array entry
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `source` | string | **yes** | Claude: `name@marketplace`; OpenCode: npm package name |
| `enabled` | bool | no | omit → enabled |
| `config` | table | no | Claude only; non-sensitive options → `pluginConfigs.<source>.options` |

## Settings — `[settings.<tool>]`

Arbitrary keys merged surgically into each tool's settings file
(`~/.claude/settings.json`; `opencode.jsonc`):

```toml
[settings.claude]
model = "opus"
theme = "dark"                            # TUI settings are top-level settings.json keys

[settings.opencode]
model = "anthropic/claude-opus-4-8"
```

Keys that collide with structures homonto manages fail at load:
`settings.claude.enabledPlugins`, `settings.opencode.mcp`,
`settings.opencode.plugin`. Because `hooks` is an ordinary Claude settings
key, you can declare tool hooks here too — see
[enforcement](enforcement.md) for the workflow guard hook.

## TUI — `[tui.opencode]`

OpenCode keeps TUI settings in a separate file
(`~/.config/opencode/tui.json`); Claude's TUI settings are plain
`[settings.claude]` keys.

```toml
[tui.opencode]
theme = "gruvbox"
scroll_speed = 3
```

## Marketplaces — `[marketplaces.claude.<name>]`

Claude-only: entries for `extraKnownMarketplaces`.

```toml
[marketplaces.claude.official]
source = "github"                  # github | url | git-subdir | directory
repo = "anthropics/claude-plugins" # for github
# url = "…"                        # for url, git-subdir
# path = "…"                       # for git-subdir, directory
# auto_update = true               # optional
```

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

[marketplaces.claude.official]
source = "github"
repo = "anthropics/claude-plugins"

[plugins.claude.claude-hud]
source = "claude-hud@official"

[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"

[settings.claude]
theme = "dark"

[settings.opencode]
model = "anthropic/claude-opus-4-8"

[tui.opencode]
theme = "gruvbox"

# Required: every framework-expanded subagent × targeted tool needs a model.
[subagents.onto.claude]
model = "opus"
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.claude]
model = "haiku"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.claude]
model = "opus"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.claude]
model = "sonnet"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5"

[subagents.onto-skeptic.claude]
model = "opus"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"

# review is explicitly declared above, so it carries its own model block too.
[subagents.review.claude]
model = "opus"
[subagents.review.opencode]
model = "anthropic/claude-opus-4-8"
```
