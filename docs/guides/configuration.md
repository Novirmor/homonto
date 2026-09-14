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
| `[workflow]` | Shared onto/to records root and Git mode | [Workflow root](#workflow-root--workflow) |
| `[repos]` | Source repository aliases | [Repos](#repos--repos) |
| `[worktrees]` | Optional execution-checkout allocation parent | [Worktrees](#worktrees) |
| `[mcps.<name>]` | MCP servers | [MCP servers](#mcp-servers--mcpsname) |
| `[skills.<name>]` | Skills (symlinked) | [Skills](#skills--skillsname) |
| `[commands.<name>]` | Slash commands | [Commands](#commands--commandsname) |
| `[subagents.<name>]` | Agent definitions | [Subagents](#subagents--subagentsname) |
| `[subagents.<name>.opencode]` | Per-subagent model override (required for every declared subagent) | [Subagent models](#subagent-models--subagentsnameopencode) |
| `[frameworks.<name>]` | Package installs (lifecycle frameworks or skill bundles) | [Frameworks](#frameworks--frameworksname) |
| `[plugins.opencode.<name>]` | OpenCode plugins | [Plugins](#plugins--pluginsopencodename) |
| `[settings.opencode]` | OpenCode settings | [Settings](#settings--settingsopencode) |
| `[tui.opencode]` | OpenCode TUI settings | [TUI](#tui--tuiopencode) |
| `[agents.<name>]` | Legacy — folds into `[subagents.<name>]` | [Legacy agents](#legacy-agents--agentsname) |

## Schema version

`schema_version` is an optional top-level key (not a table) naming the
`homonto.toml` format version. The current version is **2**.

```toml
schema_version = 2
```

Omitting it, setting `0`, or retaining `1` preserves legacy implicit-config-repo
semantics. Existing files do not opt into schema 2 on load. Schema 2 makes
`[repos]` the complete source declaration, permits external records roots,
and enables `workflow.git` and `[worktrees]`. Those last two declarations
require an explicit `schema_version = 2`.

Negative versions and versions newer than the binary supports fail at load.
Upgrade the binary for a newer config; lowering the number is not a migration.
Schema/layout and Git-mode changes while workflow state exists are guarded,
not automatically migrated. There is no workspace migration command yet.
See [workspaces](workspaces.md) before changing an existing layout.

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

Unknown fields in typed configuration sections are errors, not ignored tuning:
`workflow.rooot`, `subagents.audit.opencode.varaint`, or
`subagents.audit.step` fails at load. Diagnostics identify the selected absolute
config filename; strict unknown-field errors include the offending TOML location.
Arbitrary OpenCode settings/TUI maps remain passthrough, subject to reserved-key
and supported-tool validation.

**Config root and bootstrap.** The directory containing `homonto.toml` is the
configuration root. Run `homonto init [dir]` to scaffold that configuration;
it never runs `git init` and never installs a framework by itself. Add a
`[frameworks.onto]` or `[frameworks.to]` table, inspect `homonto plan`, then
run `homonto apply` to install the selected workflow. The config root need not
be Git. Schema 2 source declarations must already be Git checkouts, and managed
records require a separate explicit `homonto workspace init --yes` before
workflow mutations. `apply` never initializes that repository.

## Workflow root — `[workflow]`

`[workflow]` selects the shared records root for onto and to. Omit `root` to
use `docs` relative to the config file. This schema-2 example uses a separate
records repository:

```toml
[workflow]
root = "../workflow-records"
git = "managed"
```

| Field | Default | Contract |
|---|---|---|
| `root` | `docs` | In schema 2, a dedicated directory relative to the config file or an explicit absolute path, including outside the config root. |
| `git` | `existing` | Schema 2 only: `existing` uses the records' existing Git owner and manual commit policy; `managed` uses an explicitly initialized, homonto-owned standalone records repository. |

In schema 0/1, `root` must remain below the config directory; absolute paths
and `..` escapes, including through symlinks, fail at load. These legacy rules
remain unchanged.

In schema 2, records cannot contain or equal the config root, overlap its
Git/tool control paths, or overlap `worktrees.dir`. In `existing` mode they
may live in a dedicated subdirectory of a declared source, outside its control
paths. In `managed` mode they cannot overlap sources or sit inside another Git
repository. Managed initialization refuses populated or unowned repositories;
it requires your configured Git identity and runs your commit hooks without
pushing. Managed workflow mutations checkpoint their own record changes;
manual Markdown edits need a path-scoped `homonto workspace checkpoint`.

In documentation, `<workflow-root>` means this configured directory: onto uses
`changes`, `specs`, `adr`, and `guides`; to uses `tasks`. Archives remain under
the same root. Changing schema, root, or Git mode while state exists fails
closed rather than moving records, archives, locks, receipts, or recovery
packs. Ownership metadata also guards rebinding. See
[workspaces](workspaces.md) for setup, checkpoints, recovery, and current limits.

## Worktrees

Optional, schema 2 only. Declare an allocation parent before using registered
worktrees:

```toml
[worktrees]
dir = "../execution"
```

`dir` accepts a config-relative or absolute path. Omitted or empty means no
allocation parent, not a default directory. It must not contain/equal the
config root or overlap source repositories, workflow records, or reserved
control paths. Paths containing permission wildcards (`*`, `?`), backslashes,
or control characters are rejected for both `worktrees.dir` and schema-2
`workflow.root`.

`apply` renders this declaration into workspace instructions and bounded
per-alias permissions; it does not create execution checkouts. Use
`homonto worktree create` after selecting the source aliases in active workflow
state. A binding changes that change's execution path, not its `[repos]`
declaration. Changing `dir` does not retarget existing bindings.

`homonto worktree receiver` allocates a separate integration-target checkout.
Prefer early allocation, but matching terminal/archive state is supported too.
Its optional `--state-id` disambiguates archived generations from inspected
native IDs or registered `stateID` values; it cannot override existing
generation/binding evidence. Only execution `worktree create` is active-only.
See [workspaces](workspaces.md#allocate-an-integration-receiver).

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

`homonto init` does not create local skill directories. For `local:graphify`
above, create `homonto/skills/graphify/` and its `SKILL.md` before planning or
applying. Local content remains supported and user-owned.

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
| `source` | string | **yes** | `builtin:` ships the shared `homonto` coordinator plus the framework specialists (and the `h` workers); `local:` → `homonto/subagents/<name>.md`; `remote:` → pinned archive |
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

A `[frameworks.<name>]` table is the generic package/dependency mechanism for
skills, commands, subagents, and plugins that install together. The builtin
catalog ships two lifecycle frameworks, `onto` and `to`, and the `h` GitHub
skill bundle. onto and `to` are **complementary** (ADR 0042): declare either or
both — the change picks its workflow by which dispatcher the shared
`homonto` coordinator loads (ADR 0045), and both frameworks' agents project
side by side.
These builtin packages install the shared `homonto` knowledge skill and the
one shared `homonto` coordinator agent. `h` depends on onto and to: declaring
`[frameworks.h]` with `source = "builtin:h"` transitively installs both lifecycle
frameworks and satisfies their binaries' install gates. It adds the five
GitHub skills and matching commands (`/h-spike-issue`, `/h-resolve-issue`,
`/h-review-pr`, `/h-continue-pr`, `/h-review-batch`) and the read-only `h-spike`
/ `h-review` workers. `[frameworks.h]` remains the stable package key; the
skill-bundle terminology does not rename it or introduce a third lifecycle.
Beyond `builtin:`, a framework source may be `local:<path>` (a framework root
in your repo) or `remote:<url>` with a required `digest = "sha256:…"` pin.
Third-party workflow stacks are not bundled.

```toml
[frameworks.onto]        # and/or [frameworks.to] — complementary (ADR 0042)
source = "builtin:onto"
scope  = "project"
```

When the interactive installer creates a new `homonto.toml`, its project-setup
step can write the selected onto/to lifecycle declarations and, with both
binaries installed, the optional `h` GitHub skill bundle. Its model choice
populates `[settings.opencode]` and every expanded agent's model block, including
transitive dependencies. On apply the setting updates **global OpenCode
settings**, affecting other projects. Next steps reflect the configured,
installed workflows and whether `h` was selected. Review the generated values
before `homonto apply`.

## Repos — `[repos]`

Declares source aliases as `name = "<path>"`, with paths relative to the config
file or absolute. In **schema 2**, this is the complete set of available source
repositories: the config directory is not implicit. Declare it as `app = "."`
if it contains source. Each entry must exist at an exact Git worktree top-level;
two aliases may not share the same canonical Git common directory, including
through symlinks or linked worktrees. Validation fails at load.

```toml
[repos]
app = "."                  # schema 2: explicitly include the config checkout
service-a = "../service-a"
service-b = "../libs/service-b"
```

In **schema 0/1**, entries name only additional repositories; the config repo
remains implicit and cannot appear in `[repos]`. Existing unversioned and
version-1 files retain that behavior.

Projection state in `.homonto/` stays at the config root. Workflow state lives
at `workflow.root`, which can be external in schema 2. `homonto plan` names
the declared repos and `homonto doctor` reports their health. `onto new` and
`to new` select the complete change-specific source set with repeatable
`--repo <alias>` flags in schema 2; at least one is required. They do not
automatically select all declarations or infer source from the current directory.

Both `new` commands also accept repeatable `--base <alias>=<branch>` in schema 2
only. Without an override, each source contributes its current HEAD and local
branch. Select another local integration branch before record creation to leave
a dirty checkout untouched. The commit and target are frozen; subsequent
worktree creation must match both. See [workspaces](workspaces.md).

### Workflow access to declared repositories

For builtin `onto`, `to`, and `h`, `homonto apply` renders OpenCode
`external_directory` rules for the shared coordinator and implementers. Grants
cover declared sources and, in schema 2, each alias's namespace under
`worktrees.dir`, not the whole allocation parent. Only the coordinator receives
the external records-root grant; implementer denies keep external records out
even when nested in an allowed source. The coordinator owns records/checkpoints,
and implementers work on assigned source paths.

A deny rule precedes these path allows so inherited global directory permission
does not broaden the grants. Read-only specialists and custom agents receive
no such grants. Paths containing `*` or `?` are rejected because OpenCode treats
them as permission wildcards.

OpenCode authorizes external paths lexically rather than resolving symlinks.
Treat a declared repository and its links as trusted; a symlink within it can
lead outside the declared root. `[repos]` constrains agent workspace selection,
not filesystem containment against a hostile repository. Allowed scripts run
with the process's privileges; these grants and shell rules are not a sandbox
or a guarantee that every host prompt is enforced.

Changing a declared path and re-running `homonto apply` re-renders affected
agent files, but it does not migrate recorded source identities or registered
worktree ownership. Those checks can refuse a different clone or moved layout.

For a project-scoped skill, command, subagent, or MCP, `repo = "<name>"`
projects that one resource into the named declared repo. A repo-tagged skill,
command, or subagent links under that repo's `.opencode/`; a repo-tagged MCP
writes that repo's `opencode.jsonc`. The config root receives untagged
project-scoped resources. User-scoped resources, settings, TUI configuration,
plugins, and frameworks cannot target another repo. Each declared repo has a
separate `.homonto/state.<name>.json` partition at the config root, so prune,
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
| `steps` | no | Positive integer overriding this agent's OpenCode iteration budget; zero and negative values are invalid. |
| `bash_allow_add` | no | Reviewed exact-command additions; see [Settings](#settings--settingsopencode). |

The shipped coordinator and implementers use a trusted-workspace shell baseline:
inspection, setup, cloning, scripts (including Python/Node), chains, and pipes
are allowed without generic command/composition prompts. Known `git push`, GitHub
publication, and raw `gh api` patterns ask for the coordinator but are denied for
implementers, including read-only API payloads. The coordinator auto-allows local Git
operations; `git push` still asks. When `[tooling] shell_proxy = "rtk"`, it also receives
an explicit `rtk *` allow. Implementers retain prompts for destructive commands. Direct
workflow bypasses ask the coordinator for confirmation; bypasses remain denied for
implementers. Protected asks and denies follow exact additions, so `bash_allow_add`
cannot override them.
This Bash baseline deliberately overrides inherited Bash asks/denies, not edit
permissions, declared directory grants, delegation, or task write scope. Read-only
specialists remain shell- and edit-denied.

For stricter execution, use guarded custom agent definitions. `bash_default`
(`allow` or `ask`) and `bash_ask` are neutral `homonto:` frontmatter fields only,
not `homonto.toml` model-route settings; adding either to this table is an error.
An omitted baseline retains custom profiles' old guarded behavior rather than
opting them into general shell trust. See the
[neutral profile reference](subagents.md#rendered-frontmatter-the-homonto-block).

Shell permission is not publication authority. Implementers may perform
task-authorized Git/gh setup and reads within their scope; authoritative GitHub
intake, workflow state, and publication stay coordinator-owned. Verified-candidate
and shown-review-draft approval rules still bind operations inside scripts.
A tool prompt cannot override role ownership or publication approval.
Command patterns cover finite recognizable exceptions, not every risky tool;
they cannot sandbox scripts/wrappers or contain arbitrary shell directory access.

Builtin defaults are finite: **1200** steps for the shared coordinator, **300**
for implementers, and **120** for read-only specialists (including h workers).
Override in the same model block, not the declaration table:

```toml
[subagents.onto-reviewer.opencode]
model = "provider/model"
variant = "1"  # a string, if this variant exists in your provider
steps = 180
```

Rendered model and variant values are quoted YAML strings, so values such as
`"1"` stay strings rather than YAML numbers. Model identifiers reject controls
and line breaks (including escaped tabs/newlines) and `#variant` suffixes;
variants use a plain letter/digit/dot/underscore/hyphen token. Homonto checks
shape and host expressibility, not whether your provider offers that model.

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

A tune-only entry cannot set declaration fields (`scope`, `repo`, `mode`,
`targets`, `version`, or `digest`) without a `source`; they are rejected rather
than silently ignored. For example, tune `steps` under
`[subagents.onto-reviewer.opencode]`, not `step` or `steps` under
`[subagents.onto-reviewer]`.

One builtin definition can have only one host identity. Installing
`builtin:onto-reviewer` as both `audit` and `review` is unsupported, as is
declaring an alias for it alongside an onto framework that already installs
`onto-reviewer`. Tune the framework's existing name instead. A standalone alias
is valid when that builtin is not otherwise installed:

```toml
# Standalone, not alongside a framework that installs onto-reviewer.
[subagents.audit]
source = "builtin:onto-reviewer"
scope = "project"
[subagents.audit.opencode]
model = "provider/model"
steps = 180
```

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

## Workspace tmp — `[tmp]`

One declared scratch directory the whole projection shares
([ADR 0048](../adr/0048-one-declared-workspace-tmp-directory.md)). Opt-in: no
`[tmp]` table, no behavior change.

```toml
[tmp]
dir = ".tmp"   # relative, below the workspace root; default when omitted
```

| Key | Accepted | Meaning |
|---|---|---|
| `dir` | relative path | The scratch directory. Must stay below the workspace root, must name a dedicated subdirectory, and may not live inside `.git`. Defaults to `.tmp`. |

**What apply does.** Creates the directory, keeps it gitignored (an anchored
`/.tmp/` entry, or nothing under `.homonto/`, which init already ignores), and
generates `references/tmp.md` into the framework dispatchers and the shared
`homonto` knowledge skill naming the path and the contract. Editing `dir`
re-projects; removing the table withdraws the references.

**Who can write.** Every writable agent — the coordinator, the implementers,
and any custom agent — writes there with no prompts and no permission-map
changes, because the directory sits inside the workspace. Read-only workers
stay read-only by design and hand scratch to the coordinator.

**homonto never deletes tmp content.** Disabling `[tmp]` leaves the directory,
its files, and the gitignore entry in place; cleanup is a human decision.

Unknown keys and unusable paths fail at load, naming the offender:

```
tmp.path ".tmp" is an unknown key — [tmp] takes only dir
```

**Reserved paths.** The dir may not overlap the workflow records root
(`[workflow] root`), `.git`, `.opencode/` (the projection target), `homonto/`
(the local skills root), or the managed `.homonto` subtrees (`catalog`,
`remote`, `cache`) — equal to, above, or inside. A colliding scratch dir
would be gitignored or rebuilt over, taking real content with it.

**One workspace, one tmp.** The directory lives in the config repository
only; repositories declared under `[repos]` do not get one.

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
content fail at load. Protected `bash_ask` and `bash_deny` rules in the agent's
neutral profile take precedence over these additions. A private history of
executed or accepted commands is not a permission grant, and redacted placeholders
are not valid exact additions. Review real arguments locally; do not publish
private command values as examples. Even an explicitly accepted command does
not authorize posting an unapproved review draft or expanding workflow scope.

Bundled plugins (`permission-observer`) are owned catalog content: declaring
`[plugins.opencode.permission-observer]` with `source = "permission-observer"`
projects the materialized plugin path. homonto never executes it; it observes
explicit Bash approvals in memory and suggests allowlist additions exactly
once per candidate.

It reads the runtime producer's `permission.asked` request (`id`, `sessionID`,
`permission = "bash"`, `metadata.command`) and correlates `permission.replied`
by `sessionID` and `requestID`. Replies `once` and `always` count as explicit
approvals; `reject` disqualifies the command for that session. Two approvals
trigger one suggestion through `homonto permissions suggest`, whose stdin is
implicit (no `--stdin` flag). No execution event is treated as an approval.

## OpenCode integrations — `[integrations.opencode]`

The shipped `onto`/`to` lifecycle frameworks and `h` GitHub skill bundle install
a project-local, homonto-managed `.opencode/plugins/homonto-workflow.ts` link
by default. The plugin resolves the config from its materialized catalog and binding metadata,
not the session's launch directory, then reads
`homonto workflow snapshot --json --config <path>` on idle, debounced file-watcher
updates, and compaction. Reads share one in-flight request with a trailing
refresh when needed. It shows phase/task/lifecycle and health toasts; disappearance
alone is not completion. Transient subprocess, binding-discovery, or output
failures are reported separately
without discarding the last successful comparison, and a later success clears
the error. Compaction awaits a fresh result and includes pending status/findings,
or the observation error rather than stale success. This is not enforcement:
its observation hooks never run doctor, block completion, advance a phase,
record evidence, write workflow state, or send workflow data to a remote service.

The coordinator also gets `homonto_status` and `homonto_handoff` tools bound to
the exact config filename. Compaction and model-context preparation read bounded
handoffs for up to three nonterminal generations, including tasks, decisions,
artifact pointers, and validated source directories. The context cap is 16 KiB;
the 1.5-second overall deadline reports omitted or unavailable details. There is
no automatic phase advancement or resumption of a remembered publication approval.

Declaring builtin `[frameworks.h]` additionally enables the shared GitHub draft
tools: `homonto_github_draft`, `homonto_github_status`, and
`homonto_github_publish`. These support issue comments, PR comments, and
commit-bound `COMMENT`/`REQUEST_CHANGES` reviews, not pushes or PR creation.
Stage a batch, show the complete returned preview, pass its question arguments
unchanged to the native question tool, then publish the approved items by draft ID.
Decline and Revise do not publish; permissions or model-supplied approval flags
cannot replace the matching question response. Tools are denied to shipped
workers and checked against the installed coordinator name at runtime.

Drafts remain in memory, with at most ten items, 8 KiB per body, 32 KiB per batch,
and a 15-minute lifetime. Restaging invalidates older unpublished approvals;
restarting requires fresh approval and reconciliation of any interrupted send.
Headless clients without native questions cannot approve drafts. A question reply
is host-channel confirmation, not human-only attestation; trusted API clients can
answer it. The integration uses the raw-schema custom-tool compatibility and
question/context hook contracts verified against OpenCode 1.18.29/1.18.30.

Disable only that managed bridge when a project needs no runtime workflow
status, recovery tools, or GitHub draft tools:

```toml
[integrations.opencode]
workflow_bridge = false
```

The opt-out removes homonto's link but never replaces a non-homonto file at
that path. Remove or rename a hand-managed file yourself before enabling the
bridge.

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

[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"

[settings.opencode]
model = "anthropic/claude-opus-4-8"

[tui.opencode]
theme = "gruvbox"

# Required: every framework-expanded subagent needs a model.
[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5"

[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
```
