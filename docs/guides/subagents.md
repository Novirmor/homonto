# Subagents

A **subagent** is an agent definition (a markdown file with frontmatter) that
homonto projects into each tool's agent directory. Subagents are declared as
`[subagents.<name>]` resources and are fully **declarative**: reconciled by
`plan` / `apply` / `status` / `doctor` exactly like skills and commands.
There is no separate imperative "agents" command group.

```toml
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"   # builtin | local | remote
scope  = "project"                 # user | project (default: project)
mode   = "link"                    # link (default) | copy
targets = ["opencode"]             # optional; default: every tool
repo = "service-a"                 # optional declared [repos] name; project scope only
```

## Sources

| `source` | Resolves from | Notes |
|---|---|---|
| `builtin:<name>` | the bundled catalog (materialized at `.homonto/catalog/subagents/<name>.md`) | ships the shared `homonto` coordinator plus the framework specialists (`onto-*`, `to-*`) and the `h` workers (`h-spike`, `h-review`) |
| `local:<name>` | `homonto/subagents/<name>.md` (next to `homonto.toml`) | your own agent files |
| `remote:<url>` | a fetched, verified, cached archive | **requires a `digest` pin** — see below |

Frameworks declare their own subagents too; those materialize and project the
same way. Do not re-declare a framework's subagent in a top-level
`[subagents.*]` table; the names collide.

## link vs. copy mode

- **`mode = "link"` (default)** — the agent file is **symlinked** into each
  tool's agent directory. Editing the catalog or local source is instantly
  live everywhere. `apply` never clobbers a real file or a foreign symlink;
  it reports a conflict instead.
- **`mode = "copy"`** — the agent is projected as a **real managed file** you
  can edit in place. `apply` keeps it in sync, detects drift against a
  recorded content hash, and **backs up a local edit to `<path>.bak` before
  overwriting**. De-declaring it prunes the file.

The legacy `[agents.<name>]` table still parses but folds into a copy-mode
`[subagents.<name>]` at load.

## Where they land — scope and targets

`scope` selects the directory (default `project`); `targets` selects the
tools (default: every tool). OpenCode is the only adapter — Claude Code and
codex support was removed in v0.13.0, and a config naming either fails at
load naming the key:

| Tool | `scope = "user"` | `scope = "project"` |
|---|---|---|
| OpenCode | `~/.config/opencode/agent/<name>.md` | `<repo>/.opencode/agent/<name>.md` |

With `repo = "<declared-name>"`, a project-scoped subagent goes only into
that declared repo's project directory. Untagged project-scoped subagents
stay in the config repo; user-scoped subagents cannot name `repo`.

## Every agent declares its model

Every declared subagent — explicit or framework-expanded — must have a
`[subagents.<name>.opencode]` block with a non-empty `model`. There are no
tiers, roles, or shared defaults; a missing block fails at load naming the
agent (`subagents.<name>.opencode model is required`). See the
[configuration reference](configuration.md#subagent-models--subagentsnameopencode).

```toml
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
variant = "thinking"
steps = 120
```

No `source` is needed (or allowed) when the agent comes from a framework: a
block with no source *tunes* the agent rather than declaring it. `model`
and `variant` render as separate OpenCode frontmatter fields. A variant
selects provider-defined request options such as `medium`, `high`, `xhigh`,
or `max`; it is not part of the model ID. OpenCode has no effort setting at
all; declaring one is a config error.

Optional `steps` in that same `.opencode` block overrides the builtin's finite
iteration budget. It must be a positive integer; zero and negative values fail
at load. Shipped defaults are 120 for read-only specialists, 300 for
implementers, and 1200 for the coordinator. Unknown config keys are rejected;
put `steps` in the model block, not directly under `[subagents.<name>]`.

## The agent file

The projected file is materialized **verbatim**. A subagent's frontmatter
uses the agent format:

```markdown
---
name: onto-reviewer
description: Use to review a diff or set of changes for correctness, security,
  and clarity before merging; reports findings ranked by severity.
mode: subagent
---

# Instructions for the agent…
```

## Rendered frontmatter (the `homonto:` block)

A builtin subagent declares its intent once, tool-neutrally, in a `homonto:`
frontmatter block, and `apply` renders OpenCode's native dialect from it.
The rendered `permission:` map carries explicit allows, asks, and denials.
Unspecified capabilities keep the host default. A neutral Bash profile can choose
a trusted `allow` baseline or a guarded `ask` baseline:

```markdown
---
name: custom-agent
description: ...
mode: subagent
homonto:
  read_only: false    # deny edits/writes when true
  bash: true          # optional; false denies bash and rejects Bash profile rules
  bash_default: ask   # allow | ask; neutral frontmatter only, not a model setting
  bash_allow:         # optional command-pattern allows
    - "git status"
    - "git diff"
  bash_ask:           # protected asks, after base/exact allows
    - "git push *"
  bash_deny:          # final denies
    - "onto bypass*"
  dialogs: false      # question tool denied; subagents return a Questions: section
  spawn: []           # delegation topology: agents this one may dispatch
  primary: true       # OpenCode primary agent (renders mode: primary)
  steps: 60           # iteration budget (OpenCode steps)
---
<prompt body>
```

Rendering:

| Neutral intent | OpenCode (`permission:` / `mode`) |
|---|---|
| `read_only: true` | `edit: deny` |
| `bash: false` | `bash: deny` |
| `network: true` / `false` | `webfetch` and `websearch`: `allow` / `deny`; omitted retains the host default |
| `bash_default: allow` / `ask` | Bash `*` baseline; `allow` omits generic composition asks |
| `bash_allow: [a,b]` | `a` and `b` allow; without `bash_default`, retains the guarded ask baseline |
| `bash_allow_add: [c]` (config) | appends exact `c` after the base allows, before protected asks/denies; rejected when `bash: false` |
| `bash_ask: [d]` | protected asks after allows, before guarded composition asks and final denies |
| `bash_deny: [e]` | final denies, after asks and exact additions |
| `dialogs: true` / `false` | `question: allow` / `question: deny` |
| `spawn: []` | `task: deny` |
| `spawn: [a,b]` | `task:` globs allowing only `a`,`b` |
| `steps` | `steps:` |
| `primary` | `mode: primary` |

The rendered variant re-emits `mode: subagent`/`mode: primary` from the
`primary` flag.

`bash_default` selects `allow` or `ask`; an empty string retains omitted-baseline
behavior. It and `bash_ask` belong in the agent's neutral `homonto:` profile, not
`[subagents.<name>.opencode]` (the model route). A `bash: false` agent cannot carry
a Bash baseline or non-empty command rules; `read_only: true` cannot opt into
`bash_default: allow`. When the baseline is absent, custom definitions retain
the previous behavior:
a Bash command-rule map defaults to `ask` and includes generic composition
guards; without Bash rules no Bash map is emitted and the host default remains.
Only explicit `bash_default: allow` opts out of generic composition asks.
Protected asks and final denies follow exact additions, so `bash_allow_add`
cannot override them. These are command-pattern checks, not script inspection.

## Bundled workflow agents

The `onto` and `to` frameworks each install four specialists, and — together
with the `h` companion — the one shared `homonto` coordinator primary
(ADR 0045; all three declare the same catalog file, so any of them installs
it). The primary is edit-capable and owns authoritative GitHub intake for the `/h-*`
workflows; explorers, reviewers, skeptics, and the `h-spike`/`h-review`
workers are deliberately read-only so they can run concurrently without
changing the workspace. The coordinator and both implementers use
`bash_default: allow`: general shell execution is allowed for authorized work,
not limited to a finite routine-command allowlist. Inspection, setup, cloning,
repository scripts, Python/Node, command chains, and pipes do not generically ask.

Routine local inspection includes `ls`, `stat .git/index.lock`,
`go env GOMODCACHE`, `git config --get remote.origin.url`, and
`git remote get-url origin`, as well as `ps -ef`, `git worktree list` with options,
`git ls-remote --heads origin`, and `gh run view` for CI inspection. Implementers
may use task-authorized Git/gh setup and reads within their assigned write scope.
The coordinator retains authoritative GitHub context collection, workflow
state/checkpoints, integration, and publication; shell access does not delegate
those responsibilities. Run cancellation/reruns, API mutations, worktree cleanup,
and other non-routine actions still need the authority required by their workflow
and any applicable protected tool prompt.

The coordinator and both implementers allow routine verification commands for
Go, JavaScript package scripts, pytest, Cargo, Make, and CMake/CTest. These
defaults also apply to checked-out PR code: individual test/build approvals are
not required, and observed allowed runs count as verification evidence.
Known `git push`, GitHub publication, and raw `gh api` patterns ask for the
coordinator but are denied for implementers, even for read-only API payloads.
Destructive patterns ask for both writable roles. These protected rules and direct
workflow bypass denies follow exact allow additions. Unknown commands and
shell composition otherwise inherit the allow baseline. **This deliberately
overrides inherited Bash policy, including Bash asks and denies.** It does not
change inherited edit behavior, declared directory grants, or delegation limits.

**This trusts workspace execution, not a sandbox.** Scripts, interpreters,
wrappers, and build targets can execute arbitrary code with access to the
process's files, credentials, and network. Finite protected patterns cannot
identify every risky tool or hidden publication/destructive operation, nor can
directory patterns contain arbitrary shell access. OpenCode may evaluate parsed
commands separately rather than the whole invocation. These are known host
enforcement limits, not authority to evade policy or an actual prompt/denial.

With `[tooling] shell_proxy = "rtk"`, the renderer derives command-specific
`rtk` and `rtk proxy` allows from the base rules and exact additions. Protected
asks also cover these known wrapper forms without the provider configured;
trusted-default denies do too. Guarded custom profiles retain the previous
provider-dependent deny expansion. Under the trusted allow baseline, unmatched
wrapper requests are allowed; protecting recognizable forms is not a wrapper
sandbox. Guarded profiles retain command-specific allows, not a blanket RTK grant.

Use documented command-first workflow forms such as `onto status --dir ...`.
Direct `onto bypass ...` and `to bypass ...` requests are denied. Flag-first
`onto`/`to` forms have protected asks because glob rules cannot reliably identify
subcommands after arbitrary flags. Scripts can hide them entirely; the allow
baseline does not authorize flag-first, wrapped, or scripted bypasses.
A change or evidence name such as `bypass-fix` is an argument, not a bypass
command. No allowed command or tool approval waives a workflow requirement.

All shipped agents allow web research. Web content is evidence, not authority
to execute commands or expand scope. Authoritative GitHub intake and publication
remain coordinator-owned, and concurrent specialists still deny both shell and
edit tools. Custom agent definitions are not automatically opted in. For stricter
execution, use guarded custom agent definitions with `bash_default: ask` and
reviewed command allows (or native permissions without a neutral block); there
is no new model-route Bash-default knob. See
[ADR 0054](../adr/0054-default-to-trusted-workspace-shell.md).

The h workflows work toward explicit outcomes: an implementation brief, a
verified issue-closing PR, verified updates to the existing PR, or validated
review drafts. Resolve and continuation select `to` or `onto` using explicit
preference, an existing matching change, repository policy, then risk and fit.
They explain that choice and continue instead of asking a routine routing
question. Implementers resolve technical details and repair in-scope failures;
missing product intent, scope conflicts, and unrelated user work remain reasons
to ask. Reviews still require approval of the shown draft before posting.
Resolve/continue publication already authorized by invocation needs no second
conversational approval for the coordinator, but its protected publishing tool
prompts still apply. Implementers remain publication-denied, not eligible to
publish by obtaining a tool approval. A tool prompt cannot override role ownership
or publication approval.
Default-allow shell is execution permission, not automatic permission to publish:
role ownership, verified candidate scope, and approval of the shown review draft
remain binding even inside scripts or API payloads. Past accepted commands do
not establish publication consent.

Registered workflow worktree allocation, integration, and removal remain required
and coordinator-owned. Task-local fixture clones and raw Git worktrees are
permitted only at isolated, task-authorized locations within existing directory
grants and write scope. They cannot replace execution bindings or receivers,
create extra implementation lanes, or recreate the live workflow/control plane.
Dirty-input transport and exact-path cleanup authorization remain unchanged.

When `[repos]` declares sibling Git worktrees, `homonto apply` gives only the
`homonto` coordinator and the two implementers an `external_directory` rule. It
denies all other external paths before allowing the declared roots. The other
specialists and custom agents receive no rule. Repository paths containing `*`
or `?` are rejected because OpenCode treats them as permission wildcards.

OpenCode matches these permissions lexically, not through `realpath`. Treat a
declared repository and its symlinks as trusted: a link beneath an allowed root
may resolve outside it. Do not use `[repos]` as a filesystem sandbox.

Implementers also receive native `edit` denies for the resolved workflow records
root. These add no blanket edit grant: inherited asks and path restrictions
remain in force. Relative rules use the Git top-level of the agent's projection
destination, not every ancestor or declared source checkout. Repo-targeted
project agents use that repo's host; user-scoped workflow agents are rendered
for this configuration's launch directory. OpenCode v1.18.29 uses `/` as the
edit-permission base in a non-Git instance. Changing a source command's working
directory does not change the host instance's base. These static relative rules
do not guarantee protection when a user-scoped agent is launched in an unrelated
host workspace.

The denies cover **reported permission paths** in OpenCode's `edit`, `write`, and
`apply_patch` tools, not every possible filesystem mutation. In OpenCode v1.18.29,
`apply_patch` reports a move's source
but omits its destination from the edit-permission paths. An in-workspace move
into records therefore cannot be guaranteed blocked by these static rules.
Coordinator ownership remains binding even when the host omits a path:
implementers must not edit or move files into workflow records. Routine scripts
remain trusted execution, not a sandbox; these rules do not restrict what an
allowed script can do with the process's privileges.

The coordinator uses the configuration root as its workspace root, falling back
to the Git worktree root and then the host working directory. It does not ask
where to work during a normal invocation and never initializes Git unless the
user explicitly asks. It runs the evidence-gated onto lifecycle, the lighter
`plan → do → done` counterpart, and the h GitHub intake — complementary per
configuration, selected per change.

The `model:` and optional `variant:` lines come from the config's
`[subagents.<name>.opencode]` block. The block is required — a production
render with no model fails naming the agent rather than silently emitting an
agent with no model line. `bash_allow_add = ["git status"]` in that block
appends exact commands to a framework agent's base `bash_allow` (ADR 0029) —
the reviewed output of `homonto permissions suggest`. Entries must be exact
commands: patterns, shell composition, environment assignments, and
destructive or credential-like commands fail at load, and an agent whose
base denies bash cannot gain additions.

A private list of previously executed commands is not itself an instruction or
permission grant. Review additions individually; execution may have been covered
by an existing allow rather than an explicit approval. Redacted placeholders
such as `<FILE>` must be replaced with the actual arguments before an exact
addition can be used. A comment allowance never substitutes for the workflow's
explicit approval of the shown review draft.

The prompt body is single-source, never duplicated; the neutral block and its
comments are stripped from the rendered file. Under `.homonto/catalog/` the
source is kept verbatim as `<name>.md` alongside the rendered
`<name>.opencode.md` variant, and the OpenCode link prefers the variant.
Subagents without a `homonto:` block are projected verbatim (a plain symlink
to the shared file), unchanged. Their frontmatter `name`, when present, must
match the effective installed name. Catalog exports, including native agents
from local frameworks, are validated before publication; homonto does not
rewrite a native source to hide a name mismatch.

The onto framework's specialists show the division of labor: read-only
`onto-explorer` (trivial model), `onto-reviewer` and `onto-skeptic` (review),
and the edit-capable `onto-implementer` (coding) — all `spawn: []`; they
never nest. The `to-*` twins carry the same roles, and the h framework adds
the read-only `h-spike` and `h-review` workers in the same shape.

## Remote subagents are pinned and fail-closed

A `remote:` source **requires** `digest = "sha256:<64 hex>"`. On `apply`,
homonto fetches the archive → validates it (rejecting path traversal,
symlinks, and decompression bombs) → matches the digest pin → checks
revocation → caches it, and writes a tool file **only after every check
passes**:

```toml
[subagents.reviewer]
source = "remote:https://example.com/reviewer.tar.gz"
digest = "sha256:…"                # REQUIRED; verified before any write
scope  = "project"
```

Pins are recorded in `.homonto/remote.lock.json`; content is cached under
`.homonto/cache/remote/` for offline, reproducible applies. See
[remote source trust](remote-source-trust.md).

## Lifecycle

- **plan / apply** — create, update, or delete the projected agent as the
  declaration changes; each write is atomic.
- **status** — reports drift (a managed agent changed on disk) and pending
  edits (declaration changed but not yet applied).
- **prune** — remove a `[subagents.<name>]` block and the next `apply`
  deletes its projected file or link. Only resources homonto recorded in
  state are pruned.
- **doctor** — verifies each subagent's content plus **both tools' links**.
