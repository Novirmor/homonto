# homonto CLI reference

Every `homonto` command and flag. Workflow binaries: [onto](onto-reference.md),
[to](to-reference.md).

## Global behavior

- `--config <path>` (default `homonto.toml`) selects the config for every
  config-driven command. `init` takes a directory argument instead.
- Human output goes to **stderr**. JSON from `--json` / `--output json` and
  workspace-task output goes to stdout. Use `2>&1` to capture everything.
- Exit `0` on success, non-zero on error, unless `--exit-code` is noted.
- `-v`, `--version` print the version.

| Command | Purpose |
|---|---|
| [`init [dir]`](#homonto-init) | Scaffold a config directory |
| [`plan`](#homonto-plan) | Show what apply would change |
| [`apply`](#homonto-apply) | Project the config |
| [`status`](#homonto-status) | Drift and pending changes |
| [`doctor`](#homonto-doctor) | Environment health |
| [`explain`](#homonto-explain) | Why a managed resource exists |
| [`update`](#homonto-update) | Bring catalog content to this binary's version |
| [`snapshot`](#homonto-snapshot) | Undo or recover snapshot applies |
| [`permissions suggest`](#homonto-permissions-suggest) | Render `bash_allow_add` entries |
| [`cache gc`](#homonto-cache-gc) | Reclaim the remote cache |
| [`workflow`](#homonto-workflow) | Read workflow progress |
| [`workspace`](#homonto-workspace) | Records layout, checkpoints, tasks |
| [`worktree`](#homonto-worktree) | Registered per-change worktrees |
| [`tmp clean`](#homonto-tmp-clean) | Empty scratch that belongs to no active change |
| `version`, `completion <shell>` | Version; shell completion |

## `homonto init`

```console
$ homonto init                 # current directory
$ homonto init ~/dotfiles/ai   # another directory
created homonto.toml
created .gitignore
created .env.example
```

Writes a commented `homonto.toml`, a `.gitignore` with `/.homonto/` and
`.env` (appending missing lines to an existing one), and `.env.example`.
Never overwrites, never runs `git init`, never installs a package, never
creates `homonto/skills/`.

## `homonto plan`

| Flag | Effect |
|---|---|
| `--output text\|json` | Format (default `text`). |
| `--exit-code` | Exit `2` when changes are pending. |

```console
$ homonto plan
opencode:
  ~ setting.model: "anthropic/claude-opus-5-5" -> "anthropic/claude-sonnet-5-5"

$ homonto plan --output json
{
  "changes": [ { "tool": "opencode", "changes": [ { "action": "update", "key": "setting.model" } ] } ],
  "repins": [],
  "repos": [],
  "warnings": []
}

$ homonto plan --exit-code >/dev/null 2>&1 || echo "apply pending"
```

Writes nothing, resolves no secrets. With `[repos]`, lists the declared
repositories first and shows repo-tagged resources under `opencode@<alias>`.
Warnings (unverified V2 plugins, GitHub publication requirements) print
first.

## `homonto apply`

| Flag | Effect |
|---|---|
| `--yes` | Skip the `[y/N]` prompt. |
| `--snapshot` | Journal the apply so it can be undone. |

Prints the plan, confirms, resolves every secret, then writes atomically.
Guarantees: nothing is written if any secret fails; unmanaged keys survive; an
unparseable file aborts that adapter; state is saved after the adapter
succeeds; one lock covers the whole apply including declared repositories;
exact pre-existing matches are adopted without writing. Schema 2: also renders
workspace instructions and permissions, but never initializes managed records
or creates worktrees.

```console
$ homonto apply --yes
…
Applied.
```

## `homonto status`

| Flag | Effect |
|---|---|
| `--output text\|json` | Format. |
| `--exit-code` | Exit `2` on pending, `3` on drift. |

```console
$ homonto status
1 config change(s) awaiting apply (run `homonto apply`)
$ homonto status
opencode setting.model drifted (will reset on apply)
$ homonto status
No drift.
```

## `homonto doctor`

`--output text|json`. Checks that `pass` is on PATH, OpenCode's config
location exists, every skill, command, and agent has content and its link,
declared repositories exist and are Git worktrees, declared tooling providers
are detectable, and no snapshot apply is left incomplete.

```console
$ homonto doctor
warn: `pass` not found on PATH (pass: references will fail)
ok: .config/opencode (OpenCode) config location present
ok: skill "house-style" linked (opencode)
ok: subagent "homonto" linked (opencode)
ok: tooling.shell_proxy provider rtk detected
```

## `homonto explain`

```text
homonto explain [kind] [name] [--repo <alias>] [--json]
```

With no arguments, lists every managed resource. Kinds: `skill`, `command`,
`subagent`, `subagentcopy`, `setting`, `projsetting`, `tui`, `plugin`. Shows origin (direct or package), destination, the operation
that last touched it, and removal records. Never prints values.

```console
$ homonto explain skill handoff
skill         handoff              direct scope=project source=builtin:handoff
    dst: …/notes/.opencode/skills/handoff
    last: create (13f87c95, declare at 2026-10-29T14:38:10Z)
```

## `homonto update`

`--yes` skips confirmation. Re-materializes the catalog embedded in this
binary and re-projects it, printing the binary, catalog, and per-package
version transition. Does not download binaries.

## `homonto snapshot`

| Command | Effect |
|---|---|
| `snapshot list` | Committed and incomplete snapshot applies. |
| `snapshot undo <apply-id> [--yes]` | Reverse a committed snapshot apply; refuses with no change if a managed value was edited since. |
| `snapshot recover <apply-id>` | Roll back an interrupted snapshot apply. |

The latest 10 committed journals are kept.

## `homonto permissions suggest`

Reads one exact command per line from stdin and prints a `bash_allow_add`
snippet. Rejects patterns, shell composition, credentials, and destructive or
privilege-escalating commands (listed as comments). Writes nothing.

```console
$ printf 'go test ./...\nmake lint\n' | homonto permissions suggest
# reviewed suggestions — paste under the agent's [subagents.<name>.opencode] block
bash_allow_add = [
  "go test ./...",
  "make lint",
]
```

## `homonto cache gc`

`--dry-run` reports only. Deletes `.homonto/cache/remote/` entries that no
`.homonto/remote.lock.json` entry references. Not part of `apply`, so
reverting a digest can still roll back from cache.

## `homonto workflow`

| Command | Output |
|---|---|
| `workflow snapshot --json` | onto and to progress and terminal records: identities, phase, checklist progress, pending decisions and integration, findings. |
| `workflow handoff --workflow onto\|to --change <name> --identity <id> --json` | Recovery context for one exact generation: roots, validated execution directories, decisions, bounded excerpts (64 KiB/file, 12 KiB total), findings, next skill. |

| `workflow cleanup list [--workflow onto\|to] --json` | Cleanup state of every onto and to generation: status, identity, archive destination, worktrees to remove, blockers. |
| `workflow cleanup preview <change> --workflow onto\|to [--identity <id>]` | The same for one generation. |
| `workflow cleanup apply <change> --workflow onto\|to --identity <id> --yes` | Archive the terminal record, remove its clean worktrees (branches kept), and delete its `<tmp>/<engine>/<change>/` scratch. |

`snapshot`, `handoff`, `cleanup list`, and `cleanup preview` are read-only;
excerpts are data, not instructions or evidence. `cleanup` is the same
contract as `onto cleanup` and `to cleanup`, addressed through `--config`
instead of `--dir`: [cleaning up terminal workflows](workflow-cleanup.md).

## `homonto workspace`

| Command | Flags | Effect |
|---|---|---|
| `workspace inspect` | `--json` | Layout and history, read-only. |
| `workspace init` | `--yes` | Initialize an empty managed records repository (managed mode); validate the Git owner (existing mode). |
| `workspace checkpoint` | `--message` (required), `--path` (repeatable) | Commit owned record paths in managed mode. |
| `workspace recover` | — | Retry a pending checkpoint or preserve an interrupted intent's scoped delta. |
| `workspace pull` | `--branch` (required), `--repo`, `--json` | Fetch a local base branch's upstream and fast-forward it; refuses a diverged branch or a dirty checkout of it. |
| `workspace task create <name>` | `--engine`, `--repo`…, `--isolation`, `--isolation-override`, `--change` | Record a task's engine and checkouts (JSON). |
| `workspace task inspect <name>` | — | Recorded choices and check results (JSON). |
| `workspace task check <name>` | `--writers-paused`, `--check`, `--timeout` | Run matching integration checks. |
| `workspace task cleanup <name>` | `--yes` | Remove clean integrated task-owned worktrees. |

```console
$ homonto workspace inspect
Config: /work/control/homonto.toml
Workflow: /work/records
Git mode: managed
Initialized: true
Pending checkpoint: false
```

JSON adds `schema_version`, `config_root`, `workflow_root`, `worktrees_dir`,
`repos`, and `history` (`git_mode`, `initialized`, `pending`, `owned_paths`,
`git_root`, `head`). Procedures: [workspaces](workspaces.md).

## `homonto worktree`

| Command | Required flags | Effect |
|---|---|---|
| `worktree create <change>` | `--workflow`, `--repo`, `--base`, `--branch` | Create and register an execution checkout matching the change's frozen anchors. |
| `worktree receiver <change>` | `--workflow`, `--repo` (opt. `--state-id`) | Allocate a clean checkout at the recorded integration branch. |
| `worktree list` | — | Validate and list bindings. |
| `worktree remove <change>` | `--workflow`, `--repo`, `--yes` (opt. `--role execution\|receiver`) | Remove a clean binding of a terminal change whose HEAD is integrated or on its retained branch; keeps the branch. |

All accept `--json`. Text output for create/receiver/remove is the checkout
path; list prints `workflow/change`, repo, role, branch, and path. JSON
bindings include `role`, `workflow`, `change`, `repo`, `stateID`, `path`,
`branch`, `baseRef`, `baseTarget`, `baseCommit`, `ownership`, and `status`
(`ready`; interrupted operations leave `creating`/`removing`, which `list`
reports as invalid). Rules and examples: [workspaces](workspaces.md#registered-worktrees).

## `homonto tmp clean`

```console
$ homonto tmp clean            # preview
would remove onto/old-change
kept onto/add-search (active change)
Run with --yes to remove.
$ homonto tmp clean --yes
```

Empties the declared `[tmp]` directory except the scratch folders of active or
interrupted onto and to changes. Refuses when `[tmp]` is not declared.
`--json` emits `schema_version`, `tmp`, `applied`, `kept`, and `removed`.
Details: [cleaning up terminal workflows](workflow-cleanup.md#scratch).

## `homonto completion`

```console
$ homonto completion zsh > "${fpath[1]}/_homonto"
$ homonto completion bash > /etc/bash_completion.d/homonto
```

Supports bash, zsh, fish, and powershell.
