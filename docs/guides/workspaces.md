# Workspaces: multiple repositories, worktrees, and records

One `homonto.toml` can coordinate work across several Git repositories, keep
workflow records in their own repository, and run each change in an isolated
worktree. This page covers the **schema 2** layout that enables that, plus
workspace tasks, checkpoints, and recovery.

Existing configs without `schema_version = 2` keep the legacy behavior:
the config root's Git checkout is the source, `[repos]` adds siblings, and
records live under it. Changing the schema of a config that already has
workflow records is guarded, not migrated: homonto has no migration path
([ADR 0081](../adr/0081-support-only-the-live-version.md)).

## Four locations

| Location | Holds | Set by |
|---|---|---|
| **Config root** | `homonto.toml`, `.homonto/` state, project `.opencode/`. Need not be Git. | where `homonto.toml` lives |
| **Records root** | onto `changes/`, `specs/`, `adr/`, `guides/`; to `tasks/`; `.workflow/` | `[workflow] root` (default `docs`) |
| **Sources** | Your code, as Git checkouts | `[repos]` |
| **Worktrees parent** | Registered per-change checkouts | `[worktrees] dir` (no default) |

Rules: every source is the top level of a Git worktree; no two aliases share
a repository; records can't overlap the config root's control paths or the
worktrees parent; paths can't contain `*`, `?`, backslashes, or control
characters.

## Choose a layout

### A. Combined: records inside the code repository

```text
/work/app/            ← Git checkout, config root, source "app"
├── homonto.toml
└── docs/             ← records, committed with the code
/work/api/            ← Git checkout, source "api"
/work/execution/      ← worktrees
```

```toml
# /work/app/homonto.toml
schema_version = 2

[workflow]
root = "docs"
git  = "existing"      # commit records under the repo's normal policy

[repos]
app = "."              # schema 2: the config checkout must be declared
api = "../api"

[worktrees]
dir = "../execution"
```

### B. Separate: a control directory, a records repository, and sources

```text
/work/control/        ← config root (not Git)
/work/records/        ← homonto-managed records repository
/work/app/  /work/api/  ← sources
/work/execution/      ← worktrees
```

```toml
# /work/control/homonto.toml
schema_version = 2

[workflow]
root = "../records"
git  = "managed"       # homonto commits record changes automatically

[repos]
app = "../app"
api = "../api"

[worktrees]
dir = "../execution"
```

`/work/records` must be absent or empty, outside every source, and not inside
another Git repository. Initialize it explicitly:

```sh
homonto plan    --config /work/control/homonto.toml
homonto apply   --config /work/control/homonto.toml --yes
homonto workspace inspect --config /work/control/homonto.toml
homonto workspace init    --config /work/control/homonto.toml --yes
```

```console
$ homonto workspace inspect --config /work/control/homonto.toml
Config: /work/control/homonto.toml
Workflow: /work/records
Git mode: managed
Initialized: false
Pending checkpoint: false
```

`workspace init --yes` creates the repository, ownership metadata, ignore
rules, and an initial commit, using **your** Git identity and hooks. It never
adds a remote or pushes. `apply` never initializes it; `homonto init` is
unrelated (it scaffolds config).

To keep records in a repository you already maintain, use
`git = "existing"` and point `root` at its records directory. homonto won't
adopt an arbitrary repository into managed ownership.

### Legacy (schema 0/1)

```toml
[repos]
api = "../api"          # the config checkout is implicit; list only siblings
```

## Install engines

Append engines and models as usual (see [configuration](configuration.md#engines--enginesname)).
For declared repositories, apply grants the coordinator and implementers
OpenCode access to each source and its worktree namespace; only the
coordinator gets the records root. See
[repository access](subagents.md#repository-and-records-access).

## Run commands from the config root

```sh
onto state add-search --json --dir /work/control
to status --json --dir /work/control
homonto worktree list --config /work/control/homonto.toml --json
```

`onto`/`to` read `homonto.toml` under `--dir`; homonto commands take
`--config`. Pass these even when your shell is in a source worktree. Run
source edits, builds, tests, and source commits in the selected execution
checkout (the registered worktree, or the declared source).

## Start a change across repositories

In schema 2, `--repo` names the **complete** source set:

```console
$ onto new add-search --repo app --repo api --base app=develop --base api=main --dir /work/control
$ to new tune-cache --repo api --base api=main --dir /work/control
```

The workflow skills ask which branch to start from and pull it first:

```console
$ homonto workspace pull --repo api --branch main --config /work/control/homonto.toml
main: fast-forwarded (origin/main) 4f2c9e1...
```

`workspace pull` fetches the branch's upstream and fast-forwards only that
local branch. A branch checked out somewhere is updated there only when that
checkout has no uncommitted tracked changes; a diverged branch, a missing
upstream, or a failed fetch is refused, never merged or reset.

Each selected repository gets frozen anchors: a base commit and a local
integration branch. Without `--base`, that's the checkout's current HEAD and
branch. `--base alias=branch` picks another existing local branch at its
current tip, without switching or touching the dirty checkout. Tags, remote
refs, and revision expressions are not accepted.

Anchors can't be rebased or retargeted later; choose the integration target
at `new`. Unselected repositories' dirt never affects the change.

## Registered worktrees

```console
$ homonto worktree create add-search --workflow onto --repo app --base develop --branch onto/add-search --config /work/control/homonto.toml --json
$ homonto worktree create add-search --workflow onto --repo api --base main --branch onto/add-search --config /work/control/homonto.toml --json
$ homonto worktree list --config /work/control/homonto.toml
```

- `--base` must resolve to the change's frozen commit **and** target branch.
  Another branch at the same commit, or the branch after it advanced, fails.
- `--branch` must be new; existing branches and destinations are refused.
- The original checkout's HEAD, index, and files are untouched.
- Repeating an identical call returns the existing ready binding.
- Use the returned `path`; don't guess it from `worktrees.dir`.

One execution binding per workflow/change/repository. Same-repository tasks
run serially. Don't substitute a raw `git worktree add` — the gates audit
registered bindings.

### Integration receivers

A **receiver** is a separate clean checkout at the recorded integration
branch, for performing the merge:

```console
$ homonto worktree receiver add-search --workflow onto --repo app --config /work/control/homonto.toml --json
```

No `--base`/`--branch`: it attaches to the frozen target, whose tip must
contain the base. A target already checked out elsewhere is refused (with
that path); homonto won't switch or clean someone's checkout. Prefer
allocating before `onto close` / `to done`; allocation after archive works
too. With several archived generations of the same name, pick one with
`--state-id <id>` from the state or binding list. A receiver doesn't merge,
fetch, push, or record anything.

### Removing worktrees

```console
$ homonto worktree remove add-search --workflow onto --repo app --yes --config /work/control/homonto.toml
$ homonto worktree remove add-search --workflow onto --repo app --role receiver --yes --config /work/control/homonto.toml
```

Requires: terminal state (onto archived/abandoned, to done/abandoned); no
tracked, untracked, ignored, or submodule changes; no `assume-unchanged` or
`skip-worktree` entries; and the worktree HEAD contained in the recorded
target branch locally. A PR receipt alone isn't enough — squash or rebase
merges may not satisfy the ancestry check. The branch is kept. `--yes`
confirms; it bypasses nothing.

A retained binding reserves the change name, even after archive. Remove it
before reusing the name; never delete registry entries.

To do this for every worktree of a finished change at once, and to archive an
abandoned onto record, use `onto cleanup` / `to cleanup` (or
`homonto workflow cleanup`). They apply the same removal checks, keep a receipt
so an interrupted run can be retried, and keep branches:
[cleaning up terminal workflows](workflow-cleanup.md).

## Workspace tasks

`homonto workspace task` records which engine and isolation a task uses and
runs [integration checks](configuration.md#integration-checks--integrationname)
against the task's actual checkouts. It works for all three engines and is
what `no` uses instead of lifecycle records.

```console
$ homonto workspace task create api-contract --engine no --repo api --repo web --isolation worktree --config /work/control/homonto.toml
$ homonto workspace task inspect api-contract --config /work/control/homonto.toml
$ homonto workspace task check api-contract --writers-paused --config /work/control/homonto.toml
$ homonto workspace task cleanup api-contract --yes --config /work/control/homonto.toml
```

| Subcommand | Notes |
|---|---|
| `create NAME --engine no\|to\|onto --repo A… --isolation existing\|worktree` | JSON out. `--change NAME` is required for onto/to (an active native change). `--isolation-override web=worktree` per repository. Repeating with the same choices resumes; different choices are refused. |
| `inspect NAME` | JSON: recorded choices, checkout identities, check results and whether they're stale. |
| `check NAME --writers-paused` | Runs every matching check (or `--check NAME`), `--timeout` per check (default 10m). Results JSON on stdout, command output on stderr, non-zero on failure. |
| `cleanup NAME --yes` | Removes clean, integrated, task-owned worktrees and their integrated branches; keeps metadata. |

How checks pick inputs: changed repositories use their task checkout;
unchanged participants are pinned to their clean commit at task creation.
Dirt in an unchanged input needs your disposition (an approved snapshot uses
`--isolation-override alias=worktree`). Results record the check definition,
input revisions, output, exit code, and timestamps under
`.homonto/workspace-tasks/<name>/`; editing an input or the check makes them
stale. A check that modifies its inputs fails. With no matching check, the
coordinator chooses verification itself; a configured failure can't be
replaced that way.

If `create` is interrupted (for example the branch `work-<name>/<alias>`
already exists), fix the cause and repeat the identical command. An existing
branch is never adopted, even a leftover.

## Dirty work

Before any write, the coordinator shows exact dirty paths and asks once
(unless you already decided):

- **Preserve** — leave them; work within agreed scope (clean-tree gates still
  apply).
- **Isolate** — use a registered worktree from a committed base; transferring
  dirty content is a separate decision, and `.env`/secrets are never copied.
- **Specific cleanup** — an approved action on named paths. "Make it clean"
  doesn't authorize stashing, resetting, deleting, or committing your work.

## Records history (managed mode)

Every mutating `onto`/`to` command in managed mode writes a pre-write
**intent** naming exactly the record paths it may touch, runs, then commits
only changed paths in that scope. Neighbouring Markdown, other changes, and
unrelated specs are never swept in, and no empty commits are made.

Markdown you edit by hand needs an explicit checkpoint before the next state
change:

```console
$ homonto workspace checkpoint --config /work/control/homonto.toml \
    --path changes/add-search/proposal.md --path guides/search.md \
    --message "Record search proposal"
```

`--path` is relative to the records root and limited to `changes`, `tasks`,
`specs`, `adr`, `guides`, `.workflow`. Omitting it selects all owned records;
prefer explicit paths. Checkpoints refuse symlinked records, nested Git
metadata, and pre-staged work. Attribution is per path: two editors of the
same file can't be told apart.

Source commits are separate, in the source repositories. A records commit is
not evidence that code was tested or merged.

### Recovering a failed checkpoint

A Git identity or hook failure leaves the edits in place, marks a pending
checkpoint, and blocks further managed mutations:

```console
$ homonto workspace inspect --config /work/control/homonto.toml --json
$ homonto workspace recover --config /work/control/homonto.toml
```

- A **prepared** checkpoint is retried exactly; later edits, changed staging,
  or an unexpected HEAD block it.
- An **interrupted intent** checkpoints only the scoped delta since the
  pre-operation snapshot, as `Recover interrupted operation: <label>`. That
  preserves partial progress; it doesn't prove the operation completed.

Then inspect workflow state before re-running anything. Don't delete the
journal.

## Limits

- **Not a sandbox.** OpenCode checks paths lexically; symlinks in a declared
  repository can lead outside it. Allowed scripts run with your privileges.
- **Bindings are local.** Ownership binds absolute paths and Git identities.
  Moving checkouts or copying state to another machine fails ownership
  checks; don't hand-edit identities.
- **No conversion with bindings.** `to promote` and `onto demote` refuse
  changes with registered worktrees.
- **No layout migration.** Changing schema, records root, or Git mode with
  existing records fails closed.

Command details: [CLI reference](cli-reference.md#homonto-workspace).
