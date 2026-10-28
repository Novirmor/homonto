# Subagents and permissions

A **subagent** is an agent definition — a Markdown file with YAML frontmatter
— that homonto projects into OpenCode's agent directory. This page covers
declaring them, the file format, how homonto renders permissions, and what the
shipped agents are allowed to do. What each shipped agent is *for* is in the
[catalog](catalog.md#subagents).

## Declaring

```toml
# A catalog agent under its own name (also installable via a package)
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope  = "project"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"

# Your own agent: homonto/subagents/security-review.md, projected verbatim
[subagents.security-review]
source = "local:security-review"
mode   = "copy"                     # editable managed file instead of a link

# A pinned remote agent: the archive must contain reviewer.md at its root
[subagents.reviewer]
source = "remote:https://example.com/agents/reviewer.tar.gz"
digest = "sha256:<64 hex>"
```

| Field | Default | Notes |
|---|---|---|
| `source` | — | `builtin:<catalog-name>`, `local:<name>`, or `remote:<url>`. |
| `scope` | `project` | `project` → `.opencode/agent/<name>.md`; `user` → `~/.config/opencode/agent/<name>.md`. |
| `mode` | `link` | `link`: symlink to the source. `copy`: a real managed file. |
| `repo` | — | A `[repos]` alias (project scope only). |
| `digest` | — | Required for `remote:`. |
| `targets` | every tool | Only `"opencode"` is valid. |

Packages install their own agents; never redeclare a package agent in a
`[subagents.<name>]` table with a `source` — the names collide. Tune it
instead (below).

### Link vs. copy

- **`link`** — the agent file is a symlink. Edits to the source are live.
  Apply never replaces a real file or a foreign symlink at the target; it
  reports a conflict.
- **`copy`** — the agent is written as a real file you may edit in place.
  Apply keeps it in sync, detects drift by content hash, and **backs your edit
  up to `<path>.bak`** before overwriting. Removing the declaration deletes
  the file.

## Models

Every **builtin** agent needs a `[subagents.<name>.opencode]` block with a
`model`, whether a package installed it or you declared it:

```toml
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
variant = "high"
steps = 120
```

A block without a `source` *tunes* an installed agent rather than declaring
one. `model` and `variant` render as separate frontmatter fields (`variant`
selects a provider option such as `high` or `max`; it is not part of the model
ID). `steps` overrides the iteration budget and must be positive. OpenCode has
no effort setting; declaring one is an error.

`local:` and `remote:` agents are projected **verbatim** and may not carry
this block — put `model:` in the agent file's own frontmatter.

## The agent file

```markdown
---
name: security-review
description: Use to audit a diff for injection, authz, and secret handling; returns ranked findings.
mode: subagent
model: anthropic/claude-opus-5-5
---

You are a security reviewer. Read the supplied diff …
```

When a frontmatter `name` is present it must match the installed name.

## The neutral `homonto:` block

Catalog agents describe their capabilities once, tool-neutrally, in a
`homonto:` frontmatter block. Apply renders it into OpenCode's `permission:`
map and writes the result next to the source as `<name>.opencode.md`; the
projected link points at that rendered file. The block is stripped from the
output. Files without a `homonto:` block are projected unchanged.

```yaml
---
name: my-agent
description: …
mode: subagent
homonto:
  read_only: false     # true → edit: deny
  bash: true           # false → bash: deny (and no shell rules allowed)
  bash_default: ask    # allow | ask — the shell baseline
  bash_allow: ["git status", "git diff *"]
  bash_ask:   ["git push *"]
  bash_deny:  ["onto bypass*"]
  network: true        # webfetch/websearch allow (false → deny)
  dialogs: false       # false → question: deny
  spawn: []            # [] → task: deny; [a, b] → only those
  primary: false       # true → mode: primary
  steps: 60
---
```

| Neutral field | Rendered OpenCode permission |
|---|---|
| `read_only: true` | `edit: deny` |
| `bash: false` | `bash: deny` |
| `bash_default: allow` | Bash `*: allow` baseline with no generic composition asks |
| `bash_default: ask` (or omitted with rules present) | Bash `*: ask` baseline plus composition guards |
| `bash_allow` | Command-pattern allows |
| `bash_allow_add` (config) | Exact allows appended after `bash_allow` |
| `bash_ask` | Protected asks, after all allows |
| `bash_deny` | Final denies |
| `network` | `webfetch` / `websearch` allow or deny (omitted → host default) |
| `dialogs` | `question: allow` / `deny` |
| `spawn` | `task:` limited to the named agents (`[]` → deny) |
| `primary` | `mode: primary` |
| `steps` | `steps:` |

Ordering matters: protected asks and final denies come **after** the allows
(including `bash_allow_add`), so nothing you add can override them. These are
command-pattern checks; they do not inspect what a script does.

## What the shipped agents may do

| Agent | Edit | Shell | Web | Asks user | Delegates | Steps |
|---|---|---|---|---|---|---|
| `homonto` (coordinator) | yes | allow, with protected asks | yes | yes | all specialists | 1200 |
| `onto-implementer`, `to-implementer`, `no-implementer` | yes | allow, with asks and denies | yes | no | no | 300 |
| explorers, reviewers, skeptics, `h-spike`, `h-review` | no | denied | yes | no | no | 120 |
| `no-reviewer` | no | denied | yes | no | no | 160 |

### Coordinator

Trusted workspace shell: inspection, setup, cloning, builds, tests, scripts
(Python, Node, …), pipes, and chains run without prompts, including on
checked-out PR code. Local Git operations, ordinary pushes, raw `gh api`,
routine PR/issue publication, and CI reruns are allowed — within the task's
authority and after required verification and draft approval.

These still **ask**: forced or deleting pushes, `--mirror`; `gh pr merge`,
issue delete/transfer/lock; release create/edit/delete/upload; workflow
run/enable/disable, run cancel/delete; repository create/delete/edit/archive/
rename; `rm -r`/`-f`, `sudo`, `doas`, `dd`, `mkfs`; `onto bypass`, `to bypass`,
and flag-first `onto -…` / `to -…` forms; `homonto snapshot undo|recover`,
`workspace recover`, `cache gc`, `worktree remove`; and `onto cleanup apply`,
`to cleanup apply`, `homonto workflow cleanup apply`. With
`[tooling] shell_proxy = "rtk"` it also gets an `rtk *` allow, with the
protected asks still applied after it.

### Implementers

Same trusted baseline for building and testing within their assigned
repository. **Asked:** destructive commands (`rm -r/-f`, `sudo`, `dd`, …) and
history-rewriting or work-discarding Git (`reset`, `clean`, `rebase`,
`commit --amend`, `checkout -- …`/`-f`/`.`, `restore`, `branch -d/-D`,
`worktree remove/prune`). **Denied:** `onto`, `to`, `homonto` (workflow state
is the coordinator's), any `git push`, `gh api`, and every `gh` command that
publishes or administers (PR/issue comment, create, review, edit, close, merge;
releases; workflow runs; repository admin).

### Read-only specialists

`edit: deny`, `bash: deny`, `question: deny`, `task: deny`. They can read
files and the web. They return findings, `Questions:`, and
`Evidence requests:`; the coordinator runs any probe they need. Because they
cannot change anything, the coordinator runs many at once.

### This is trust, not a sandbox

An allowed script or build can run arbitrary code with your files,
credentials, and network. Pattern rules cannot see inside scripts or
wrappers, and OpenCode may evaluate parsed commands separately. Saved OpenCode
approvals can satisfy an `ask` but never override a `deny`; homonto neither
imports nor deletes them. Shell permission is not publication authority: the
skills still require verification and explicit approval of shown review
drafts. For stricter execution, write your own agent with
`bash_default: ask` and a reviewed `bash_allow` list.

## Repository and records access

When `[repos]` is declared, apply gives each builtin coordinator and
implementer an OpenCode `external_directory` rule: deny every external path
first, then allow the declared repositories, their namespaces under
`worktrees.dir`, and the selected skill roots. Only the coordinator gets the
workflow records root; implementers are explicitly **denied** edits there,
even when records sit inside an allowed repository. Read-only specialists may
read declared paths. Custom agents receive no rules.

Records-edit denies are written relative to the projection's launch directory
for paths inside it (OpenCode V2 reports in-instance edits relative to it) and
absolute for paths outside. They cover the paths OpenCode reports for `edit`,
`write`, and `apply_patch`; OpenCode v1.18.29's `apply_patch` omits a move's
destination, so an in-workspace move into records is not guaranteed blocked.
The coordinator-owns-records rule binds implementers regardless.

OpenCode matches paths lexically, not through `realpath`, so a symlink in a
declared repository can lead outside it. Declare only trusted repositories.

## Lifecycle

- `plan` / `apply` create, update, and delete the projected agent.
- `status` reports drift (projected file changed) and pending (declaration
  changed).
- Removing the declaration prunes the projected file or link.
- `doctor` checks each agent's content and link.
- `explain subagent <name>` shows its origin and last change.

Remote agents follow the [remote source](remote-sources.md) pipeline: fetch,
validate, digest match, revocation check, cache — all before any write.
