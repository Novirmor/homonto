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
| `builtin:<name>` | the bundled catalog (materialized at `.homonto/catalog/subagents/<name>.md`) | ships the `onto` primary orchestrator and its specialists, or the parallel `to` primary orchestrator and its specialists |
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
```

No `source` is needed (or allowed) when the agent comes from a framework: a
block with no source *tunes* the agent rather than declaring it. `model`
and `variant` render as separate OpenCode frontmatter fields. A variant
selects provider-defined request options such as `medium`, `high`, `xhigh`,
or `max`; it is not part of the model ID. OpenCode has no effort setting at
all; declaring one is a config error.

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
OpenCode **denies by exception** — a `permission:` map carries the denials —
so a neutral denial removes the capability, and every capability the intent
does not deny keeps the tool's default (nothing is silently stripped by an
allowlist):

```markdown
---
name: onto-implementer
description: ...
mode: subagent
homonto:
  read_only: false    # deny edits/writes when true
  bash: false         # optional; false denies bash (default: allowed)
  bash_allow:         # optional; other Bash commands ask
    - "git status*"
    - "git diff*"
  dialogs: false      # question tool denied — subagents return a Questions: section
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
| `bash_allow: [a,b]` | `bash:` rules: `*` asks; `a` and `b` allow |
| `bash_allow_add: [c]` (config) | appends `c` after the base list, deduplicated; rejected when `bash: false` |
| `dialogs: true` / `false` | `question: allow` / `question: deny` |
| `spawn: []` | `task: deny` |
| `spawn: [a,b]` | `task:` globs allowing only `a`,`b` |
| `steps` | `steps:` |
| `primary` | `mode: primary` |

The rendered variant re-emits `mode: subagent`/`mode: primary` from the
`primary` flag.

## Bundled workflow primaries

The `onto` and `to` frameworks each install a selectable primary agent plus
four specialists. The primary is edit-capable; explorers, reviewers, and
skeptics are deliberately read-only so they can run concurrently without
changing the workspace. A primary's documented Git, test, and workflow commands
are allow-listed. Other shell commands ask rather than receiving a blanket
shell grant.

Both primaries use the configuration root as their workspace root, falling back
to the Git worktree root and then the host working directory. They do not ask
where to work during a normal invocation and never initialize Git unless the
user explicitly asks. `onto` runs the evidence-gated lifecycle; `to` is its
lighter `plan → do → done` counterpart. They are mutually exclusive per
configuration.

The `model:` and optional `variant:` lines come from the config's
`[subagents.<name>.opencode]` block. The block is required — a production
render with no model fails naming the agent rather than silently emitting an
agent with no model line. `bash_allow_add = ["git status"]` in that block
appends exact commands to a framework agent's base `bash_allow` (ADR 0029) —
the reviewed output of `homonto permissions suggest`. Entries must be exact
commands: patterns, shell composition, environment assignments, and
destructive or credential-like commands fail at load, and an agent whose
base denies bash cannot gain additions.
The prompt body is single-source, never duplicated; the neutral block and its
comments are stripped from the rendered file. Under `.homonto/catalog/` the
source is kept verbatim as `<name>.md` alongside the rendered
`<name>.opencode.md` variant, and the OpenCode link prefers the variant.
Subagents without a `homonto:` block are projected verbatim (a plain symlink
to the shared file), unchanged.

The onto framework's specialists show the division of labor: read-only
`onto-explorer` (trivial model), `onto-reviewer` and `onto-skeptic` (review),
and the edit-capable `onto-implementer` (coding) — all `spawn: []`; they
never nest. The `to-*` twins carry the same roles.

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
