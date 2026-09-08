# homonto CLI reference

Every command, flag, and exit-code contract of the `homonto` binary. For the
workflow binaries see the [onto reference](onto-reference.md) and the
[to reference](to-reference.md).

Global behavior:

- `--config <path>` (persistent flag, default `homonto.toml`) selects the
  config file for config-driven commands, including `workspace` and `worktree`.
  `init` instead takes an optional target directory argument.
- Human-readable command output uses **stderr** (cobra's print default).
  `workspace inspect --json` and `worktree` commands with `--json` write to
  stdout. Redirect with `2>&1` when capturing both streams in scripts.
- Unless `--exit-code` is noted below, commands exit `0` on success and
  non-zero on error.

## `homonto init [dir]`

Scaffold a starter repo: `homonto.toml`, `.gitignore` (excluding `.homonto/`),
`.env.example`, and `homonto/skills/`. Writes into `dir` (default: the current
directory) and **never overwrites** an existing file.

This scaffolds configuration, not Git history. For an empty managed records
repository, use `homonto workspace init --yes` after configuring its layout.

```console
$ homonto init
$ homonto init ~/dotfiles/ai     # scaffold somewhere else
```

## `homonto plan`

Print the diff between the desired state (`homonto.toml`) and what is on
disk. `plan` writes nothing and **never resolves or prints a secret**;
references stay `${…}` tokens.

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |
| `--exit-code` | opt-in exit taxonomy: exit `2` when changes are pending |

The diff is Terraform-style: `+` create, `~` update, `-` delete. Unchanged
keys stay silent.

With `[repos]`, plan first lists every declared repository. Untagged
project-scoped resources remain in the config repo; `repo = "<name>"`
resources render in a separate `opencode@<name>` changeset. This is the full
write scope for a later apply.

```console
$ homonto plan
opencode:
  ~ setting.model: "anthropic/claude-opus-4-8" -> "anthropic/claude-sonnet-5"

$ homonto plan --output json | jq .        # machine-readable
$ homonto plan --exit-code && echo clean   # CI: fail when an apply is pending
```

## `homonto apply`

Project the config into the tools: print the plan, confirm (`[y/N]`), then
write.

Schema-2 workspace declarations also render workspace instructions and bounded
external-path permissions. `apply` does not initialize managed records Git or
allocate execution worktrees. See [workspaces](workspaces.md).

| Flag | Effect |
|---|---|
| `--yes` | skip the confirmation prompt |

Guarantees (details in [projection & state](projection-and-state.md) and
[secrets](secrets.md)):

- **Two-phase.** Every secret resolves up front, before any file is written.
  One failed resolution aborts the whole apply untouched.
- **Atomic writes.** Each file is written via temp + rename, so an
  interrupted run never leaves a half-written file.
- **Surgical.** Only managed keys are written; unmanaged keys survive. An
  unparseable tool file makes that adapter abort and report rather than
  overwrite.
- **State per adapter.** State is saved after each successful adapter, so a
  failure partway through never loses an already-applied adapter's records.
- **Declared-repo isolation.** One config-repo apply lock covers the entire
  plan. Each declared repo records its resources in
  `.homonto/state.<name>.json`; no undeclared repo is read or written.
- **Adoption.** A declared resource that already exists on disk exactly as
  homonto would write it is recorded into state with no file write.

## `homonto status`

Compare managed values on disk against the last-applied snapshot and report
two independent things:

- **Drift** — a managed value changed on disk *outside homonto*, or was
  deleted: `opencode setting.model drifted (will reset on apply)`.
- **Pending** — unapplied `homonto.toml` edits, reported as a count:
  `1 config change(s) awaiting apply (run `homonto apply`)`.

When neither is present it prints `No drift.`

Drift from a declared repo is labelled `opencode@<name>`, so a reset is
attributed to the repository containing the changed file.

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |
| `--exit-code` | opt-in taxonomy: exit `2` on pending, `3` on drift |

## `homonto doctor`

Environment health check: is `pass` on `PATH`, do the tool config locations
exist, and does each owned skill have intact content plus both tool links?
Declared repos are rechecked for existence and Git-worktree status.

| Flag | Effect |
|---|---|
| `--output text\|json` | output format (default `text`) |

## `homonto update`

Re-materialize this binary's embedded catalog (frameworks, skills, commands,
subagents) and re-project it, bringing installed content up to the running
version. Prints the version transition (binary, catalog, per-framework) and
shares apply's plan → confirm → apply flow.

| Flag | Effect |
|---|---|
| `--yes` | skip the confirmation prompt |

`update` does **not** download or replace the binaries themselves. Install
those the usual way (`go install …@latest`, the interactive installer, or the
release archives), then run `homonto update`. State records the versions
behind each apply, and `onto doctor` / `to doctor` warn when a workflow binary
and the homonto that installed its framework have drifted apart.

## `homonto cache gc`

Reclaim entries in the content-addressed remote cache
(`.homonto/cache/remote/`) that no `.homonto/remote.lock.json` entry
references. Kept out of `apply` on purpose, so reverting a `digest` pin can
still roll back from cache. See
[remote source trust](remote-source-trust.md).

## `homonto explain [kind] [name] [--repo <alias>] [--json]`

Why each managed resource exists. `homonto explain` lists everything; a
`kind` (`skill`, `command`, `subagent`, `subagentcopy`, `mcp`, `projmcp`,
`setting`, `projsetting`, `tui`, `plugin`) and `name` select one resource,
with `--repo` to disambiguate across repository partitions. Each row shows
origin (direct declaration or framework+provider), destination, the
operation that last touched it, and — for removed resources — the removal
record. Values are never shown, so secrets cannot leak. Unknown selectors
fail nonzero naming the valid kinds; ambiguous names list the partitions.

## `homonto snapshot undo <apply-id>` / `recover <apply-id>` / `list`

The reverse side of `homonto apply --snapshot` (ADR 0030). `undo` restores a
committed snapshot's managed state, links, copies, and structured keys —
refusing with zero mutation if any managed value changed after the apply.
`recover` rolls back an interrupted snapshot apply (a killed process cannot
hold the process lock, so recovery always starts). `list` shows snapshots and
their status; doctor reports incomplete ones with the exact recover command.
Retention keeps the latest 10 committed journals.

## `homonto permissions suggest`

Reads one exact command per line from stdin and renders a
`bash_allow_add = [...]` TOML snippet for `[subagents.<name>.opencode]`
(ADR 0029). Patterns, shell composition, credentials, and destructive or
privilege-escalating commands are rejected inline. Writes nothing — paste
the snippet, review, and keep.

Stdin is implicit; there is no `--stdin` flag. The optional permission-observer
plugin correlates runtime `permission.asked` (`id`, `sessionID`, `permission`,
`metadata.command`) with `permission.replied` (`sessionID`, `requestID`, `reply`).
Two explicit `once`/`always` approvals can produce one suggestion; `reject`
disqualifies the command for that session. Execution alone is not approval.

## `homonto workflow snapshot --json`

Print a read-only JSON snapshot of `onto` and `to` progress and terminal records
for the selected `--config`. It includes durable identities, lifecycle status,
phase, checklist progress, pending decisions/integration, and concise findings
for malformed or unreadable state. A missing workflow is empty; invalid layout
or unreadable state is a finding, not proof of completion. Consumers must inspect
findings and status rather than treat a successful snapshot command as a passed
doctor check. The bundled OpenCode bridge consumes it but never mutates workflow
state or enforces gates.

## `homonto workspace`

Inspect the configured records layout and manage its Git checkpoints. These
commands accept the global `--config <path>` flag and no positional arguments.
From a source checkout, pass the control config's full path. See
[workspaces](workspaces.md) for complete layout examples.

| Command | Flags | Effect |
|---|---|---|
| `workspace inspect` | `--json` | Read-only layout and history inspection; does not initialize Git, acquire a history lock, or audit source dirt. |
| `workspace init` | `--yes` required | Explicit initialization of an absent/empty managed records root; in `existing` mode, only validates its existing Git owner. |
| `workspace checkpoint` | `--message <text>` required; repeatable `--path <path>` | Commit selected owned record files/subtrees in managed mode. |
| `workspace recover` | none | Retry an exact prepared checkpoint, or preserve only the scoped delta of an interrupted pre-write intent with a recovery label. |

Inspection text reports `Config`, `Workflow`, `Git mode`, `Initialized`, and
`Pending checkpoint`. JSON includes `schema_version`, `config_path`,
`config_root`, `workflow_root`, `worktrees_dir`, `repos`, and `history`.
History includes `git_mode`, `initialized`, `pending`, and `owned_paths`, plus
`git_root` and `head` when available. An uninitialized managed root is a valid
inspection result; an invalid existing Git owner or ownership mismatch is an
error. Inspection never adopts a repository.

Managed initialization writes ownership metadata, ignore rules, and an initial
commit. It requires your configured author/committer identity, runs your Git
commit hooks, and never pushes. A repeat init validates existing ownership;
it does not adopt populated or unowned directories. `homonto apply` does not
perform this initialization.

Checkpoint paths are literal, workflow-root-relative paths under `changes`,
`tasks`, `specs`, `adr`, `guides`, or `.workflow`. Omission selects all owned
records, not the whole Git tree. Prefer exact paths after diff inspection:

```bash
homonto workspace checkpoint --config /work/control/homonto.toml --path changes/add-search/proposal.md --path guides/search.md --message "Record search design"
```

The message must be nonempty. Checkpoints exclude transient files, refuse
symlinked records/nested Git metadata and pre-staged work, and do not create
empty commits. Managed onto/to mutations persist pre-write intent with an
explicit operation write scope and checkpoint only changed paths in that scope.
State setters do not sweep neighboring Markdown, other changes, or unrelated
specs/guides into history. Manual Markdown edits still need an explicit
checkpoint; source commits remain separate. Attribution is path-level: arbitrary
editors in the same owned file or subtree cannot be distinguished, and selected
files are committed whole. Inspect existing edits and avoid concurrent writes
inside an operation's scope.

Managed archive intent also contains the prepared source, exact destination,
date, and source-state fingerprint. The handler consumes that same plan, checks
source directory/state identity before writing, then rechecks directory/parent
identity and target absence before the move. It does not recompute an archive
date or suffix after gates, so midnight cannot split the journal's scope from
the actual archive. Replaced sources or occupied destinations fail closed.

Checkpoints use Git-normalized content, including normal CRLF conversion from
Git configuration or attributes, without rewriting working files. Recovery
separately protects the original raw bytes: later edits still block it even
when they would normalize to the same committed content. A normalization change
that alters the pending commit also blocks recovery.

A failed history commit returns nonzero even if the workflow operation already
completed; filesystem edits remain. A pending journal blocks subsequent managed
mutations. Fix the Git identity/hook issue and run `workspace recover`, not the
possibly completed workflow command. For a prepared checkpoint, recovery refuses
changed pending content, unrelated staging, and unexpected HEAD movement; it can
acknowledge an exact commit left pending by interrupted journal cleanup. For an
interrupted pre-write intent, recovery compares current scoped files with the
stored pre-operation snapshot and records `Recover interrupted operation: <label>`.
It neither sweeps unrelated paths nor claims the operation completed; later edits
inside the scope cannot be attributed to an editor. A no-change intent creates
no empty commit. Inspect workflow state afterward. Recovery never pushes or
rolls back user edits. There is no `workspace migrate` command.

## `homonto worktree`

Manage registered per-workflow/change/repo execution checkouts using the global
`--config <path>` flag. Requires an explicit `worktrees.dir` for allocation;
there is no default parent or per-task binding.

| Command | Flags | Effect |
|---|---|---|
| `worktree create <change>` | Required: `--workflow onto\|to`, `--repo <alias>`, `--base <ref>`, `--branch <new-name>`; optional `--json` | Create and register an isolated checkout for an alias selected by matching active state. |
| `worktree receiver <change>` | Required: `--workflow onto\|to`, `--repo <alias>`; optional `--state-id <id>`, `--json` | Allocate a clean checkout at the recorded local integration target from matching active or terminal/archive state. `--state-id` disambiguates archived generations; it cannot override existing bindings. No `--base` or `--branch` flags. |
| `worktree list` | `--json` | Read-only validation and listing of execution and receiver bindings. |
| `worktree remove <change>` | Required: `--workflow onto\|to`, `--repo <alias>`, `--yes`; optional `--role execution\|receiver` (default `execution`), `--json` | Remove only the selected clean, owned, integrated role for terminal workflow state. Retains the branch. |

Creation resolves the explicit base to a commit and integration target. For
records with frozen source anchors, both must match: even another branch at
the same commit, or an advanced tip of the chosen branch, is a mismatch.
Choose alternatives before record creation with the schema-2-only, repeatable
`onto new` / `to new --base <alias>=<branch>` flag; omission uses each source's
current HEAD and local branch. Worktree creation cannot rebase those anchors.
It refuses existing branch names or destinations, leaves
the declared checkout's HEAD/index/files untouched, and reuses an identical
ready binding on repeat calls. Use the returned path for source edits and
tests; keep `onto`/`to` commands on `--dir <configRoot>`.

Receiver allocation uses the recorded local branch and immutable base (or, for
legacy state lacking per-repo anchors, a validated execution binding's local
target). The target's tip must contain that base. It refuses a target checked
out anywhere else, even clean, naming that checkout; it never adopts, switches,
or cleans it. Prefer early allocation, but post-archive allocation/recovery is
supported. Existing workflow/change bindings pin the generation across aliases
and roles; otherwise matching state at the active path takes precedence, with
one unambiguous terminal archive accepted only when that path is absent.
`--state-id` accepts a native state `id` or registered `stateID` from inspected
records to disambiguate archives. It must match existing generation/binding
evidence, cannot bypass a reused active name, and never changes ownership or
base/target anchors. Repeat allocation validates the existing receiver and its
cleanliness. A receiver does not replace execution authority, merge source,
fetch, publish, or record completion. `worktree create` remains active-only.

Create/receiver/remove text output is the checkout path. List text columns are
`workflow/change`, repo alias, role, branch, and path. JSON create/receiver/remove
output is a binding object; list output is an array sorted by
workflow/change/repo/role. Fields include `role`, `workflow`, `change`, `repo`,
`stateID`, `workflowRoot`, `parent`, `path`,
`commonDir`, `gitDir`, `ownership`, `branch`, `baseRef`, `baseTarget`, `baseCommit`,
and `status`. Successful bindings have status `ready`. Interrupted operations
may retain `creating` or `removing` entries; list fails on incomplete or invalid
bindings instead of returning them as usable. There is no `worktree recover`
command or automatic adoption/deletion of interrupted allocations.

Roles are `execution` and `receiver`; an older binding with omitted `role`
means execution. Each role has one binding per workflow/change/repo and can be
removed independently under the same safety checks, in either order.

Removal needs terminal state (onto archived/abandoned; to done/abandoned), no
tracked, untracked, ignored, or submodule dirt, and current worktree HEAD as an
ancestor of the recorded integration target. Removal also refuses index entries
marked `assume-unchanged` or `skip-worktree`, including sparse-checkout entries,
even if status looks clean. A PR receipt alone cannot satisfy the ancestry test;
squash/rebase integration may not preserve it. `--yes` does not bypass any check,
and removal neither fetches/merges source nor deletes branches.

A retained binding blocks reuse of its workflow/change name after archive or
abandonment, even if a new change selects different repos. Remove or recover
the binding first; do not discard registry entries to free the name.

Registered ownership binds local absolute paths, source Git identity, and
workflow state identity. Changing configuration does not rebind it. `to promote`
and `onto demote` refuse affected registered bindings because conversion cannot
transactionally rebind them. Do not delete registry entries to force conversion.
Active bindings also cannot be removed, so removal is not an active-conversion
route. Continue in the current workflow; no ownership-safe active rebind API
or automatic layout migration is available.

## `homonto version`

Print the release-stamped build version (`homonto --version` works too).

## `homonto completion <shell>`

Generate a shell autocompletion script (bash, zsh, fish, powershell), a
standard cobra facility:

```console
$ homonto completion zsh > "${fpath[1]}/_homonto"
```
