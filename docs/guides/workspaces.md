# Workspaces and source repositories

Use one configuration to coordinate work across several Git repositories,
with workflow records either alongside source or in a separate repository.
Choose this layout for a new workspace with `schema_version = 2`. Existing
schema 0/1 configurations retain their legacy behavior; changing the version
does not migrate their records. The layout decision is recorded in
[ADR 0052](../adr/0052-separate-workflow-history-from-source-worktrees.md).

## The four locations

| Location | Purpose |
|---|---|
| Config root | Directory containing `homonto.toml`, projection state in `.homonto/`, and project-local OpenCode configuration. It need not be Git. |
| `workflow.root` | Shared records: onto's `changes/`, to's `tasks/`, plus `specs/`, `adr/`, `guides/`, and `.workflow/`. Archives stay here. Defaults to `docs`. |
| `[repos]` | Explicit aliases for source Git checkouts. Schema 2 has no implicit config-root source. |
| `worktrees.dir` | Optional parent for registered execution checkouts. Omit it if you do not need worktree isolation. There is no allocation default. |

Relative paths resolve from the config file, not your shell's working directory.
Schema 2 also permits explicit external paths for records and worktrees,
including absolute paths. Each source must exist at an exact Git worktree
top-level. Two aliases cannot identify the same Git common directory, even
through symlinks or different linked worktrees.

The layout validator rejects overlaps with Git/tool control files and between
records and the worktree allocation parent. An `existing` records root may be
a dedicated subdirectory of a declared source. A `managed` records root must
be a standalone Git root outside all source repositories and cannot be nested
inside another Git repository. See the [configuration reference](configuration.md)
for path rules.

## Choose a layout

### Combined source and records

For an existing Git checkout at `/work/app`, use `/work/app/homonto.toml`:

```toml
schema_version = 2

[workflow]
root = "docs"
git = "existing"

[repos]
app = "."
api = "../api"

[worktrees]
dir = "../execution"
```

Both `/work/app` and `/work/api` must already be Git checkouts. `app = "."`
explicitly selects the config checkout as an available source. Records live
in `/work/app/docs`; you commit them under that repository's existing commit
policy. In a combined checkout, task bookkeeping can share a source commit.
`existing` mode does not initialize Git or make automatic records commits.

### Non-Git control directory, separate records

For a non-Git directory at `/work/control`, use `/work/control/homonto.toml`:

```toml
schema_version = 2

[workflow]
root = "../records"
git = "managed"

[repos]
app = "../app"
api = "../api"

[worktrees]
dir = "../execution"
```

The control directory holds configuration and projection state. Source stays
in the two existing checkouts. `/work/records` starts absent or empty, and
`/work` must not itself be inside a Git repository. Homonto can initialize
that records directory only through the explicit command below.

To use a separate records repository you already maintain, choose
`git = "existing"` and point `root` at its records directory instead. Homonto
does not adopt an arbitrary existing Git repository into managed ownership.

### Install the workflow

Append your framework and model declarations to either layout. For example,
this installs onto; replace model identifiers with ones available in your
OpenCode installation:

```toml
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
```

For the separate managed example:

```bash
homonto plan --config /work/control/homonto.toml
homonto apply --config /work/control/homonto.toml --yes
homonto workspace inspect --config /work/control/homonto.toml --json
homonto workspace init --config /work/control/homonto.toml --yes
```

`apply` installs the declared framework and generated workspace instructions;
it does **not** initialize managed history. `workspace init --yes` creates the
empty records repository, ownership metadata, ignore rules, and initial
commit. It requires your configured Git author and committer identity, uses
your Git hooks, and does not set a substitute identity, bypass commit hooks,
configure a remote, or push. A hook failure can leave a pending initial
checkpoint; use the recovery procedure below.

`homonto init` is a different command: it scaffolds configuration without
running `git init`. Do not initialize Git in the control directory to repair
a records or source error.

## Select all sources for a change

In schema 2, `--repo` names the **complete source selection**, not additional
repositories alongside an implicit config repo. Both `onto new` and `to new`
require at least one declared alias. Declare repositories once, then select
only those involved in each change:

```bash
onto new add-search --repo app --repo api --base app=develop --base api=main --dir /work/control
```

Or, with the `to` framework installed, start a different task:

```bash
to new tune-cache --repo api --base api=main --dir /work/control
```

An unselected repo's dirt does not enter that change's source clean gate.
In schema 2, both workflows record source Git identity and freeze a base commit
and local integration branch for each selected repo. Without `--base`, creation
uses that checkout's current HEAD and attached local branch.

Repeat `--base <alias>=<branch>` to choose another existing local branch and
its current tip **before creating the record**. Each alias must be selected
with `--repo` and may have only one override. Tags, remote-tracking refs, and
revision expressions are not local branch selections; schema 0/1 rejects
`--base`. The examples choose `develop` for app and `main` for api without
switching the dirty source checkouts or changing their indexes or files.

These anchors cannot be rebased or retargeted afterward. Onto's per-repo
`base-ref` and `base-branch` setters validate frozen anchors rather than changing
them. Choose the intended integration target at `new`, not at worktree creation.

Source identity uses the canonical absolute Git common-directory path. A
linked execution worktree can share that identity; another clone cannot.
These are local bindings, not portable repository IDs. Moving checkouts,
copying control state to another machine, or repointing an alias can fail
ownership checks. Do not hand-edit recorded identities to silence a refusal.

## Run commands in the right directory

Workflow commands use the **config root**, even when your editor or shell is
inside a source worktree:

```bash
onto state add-search --json --dir /work/control
onto dirt add-search --json --dir /work/control
to status --json --dir /work/control
homonto workspace inspect --config /work/control/homonto.toml --json
homonto worktree list --config /work/control/homonto.toml --json
```

`onto` and `to` currently load `homonto.toml` under `--dir`. They do not select
a differently named config file through homonto's `--config` flag. Do not
pass the records root or a source checkout as `--dir` to obtain a clean view
of a different workspace.

Run source edits, Git, builds, tests, and source commits in each selected
execution checkout. That is the registered worktree if one exists for the
change and alias, otherwise the declared source checkout. Keep `[repos]`
pointing at the declared source; do not rewrite it to an execution path.

Combined-layout clean gates differ by workflow. Onto blocks its own change
records but exempts other changes/archives only in the actual source checkout
owning records; a separate execution checkout has no such carve-out. To in
explicit `existing` mode excludes the configured records subtree, including
its counterpart in a bound checkout. Neither treats independent records Git
as an extra source. Record history still follows its own checkpoint/commit
policy. See the workflow references for the complete gates.

## Isolate without discarding dirty work

The shared policy covers all shipped skills and delegated tasks, including
direct skill entry and resume. Before writes, inspect status, staged and
unstaged diffs, and untracked files in affected Git roots. Inspect non-Git
destinations directly. Present exact paths and honor the user's existing
choice, or ask once if no choice covers them:

- **Preserve:** leave existing edits intact and work within the agreed scope.
  Preservation does not waive a clean-tree gate.
- **Isolate:** create a registered worktree from an explicit committed base.
  The original checkout's HEAD, index, and dirty files remain untouched. Dirty
  content is not in that base; transferring needed paths requires a separate
  choice. Do not automatically copy secrets, `.env`, or untracked files.
- **Specific cleanup:** obtain approval for the named action and paths,
  including a destination for moved files. A request for a clean tree does
  not authorize stashing, resetting, deleting, or committing user changes.

Read-only research can continue while a choice is pending. Carry the choice
into handoffs and delegated tasks; ask again only for a new conflict.

After creating active state with the selected aliases, allocate one checkout
per needed repo. For the `add-search` example above, the selected base branches
must still point at their frozen commits, and the new branch names must not
already exist:

```bash
homonto worktree create add-search --workflow onto --repo app --base develop --branch onto/add-search --config /work/control/homonto.toml --json
homonto worktree create add-search --workflow onto --repo api --base main --branch onto/add-search --config /work/control/homonto.toml --json
homonto worktree list --config /work/control/homonto.toml --json
```

Use `--workflow to` for a to task. Read the returned `path` rather than
guessing it from `worktrees.dir`. Creation requires both the resolved commit
and target branch to match the record's frozen anchors. A different branch
at the same commit, or the chosen branch after its tip advances, fails this
check; passing a bare commit SHA does not supply the required branch target.
Creation also refuses existing branches or destinations.
Repeating the same ready binding with the same base and branch is idempotent.

There is one execution binding per workflow/change/repo, not per implementation task.
Run same-repo implementation tasks serially. Do not substitute raw
`git worktree add` or an unregistered clean checkout to evade source gates.

## Allocate an integration receiver

When integration needs its own clean target checkout, prefer allocating a
**receiver** before `onto close` or `to done` so target-checkout conflicts surface
early. Identity-checked terminal/archive allocation is also supported for
post-archive integration or recovery; only execution creation is active-only:

```bash
homonto worktree receiver add-search --workflow onto --repo app --config /work/control/homonto.toml --json
homonto worktree list --config /work/control/homonto.toml --json
```

The command takes no `--base` or `--branch`: it attaches to the recorded local
integration branch, whose current tip must contain the frozen base. Legacy
state may use the recorded local target of an execution binding when it lacks
per-repo anchors; no default branch is guessed. It requires a selected alias,
valid ownership, and `worktrees.dir`. A target already checked out anywhere,
even clean, is refused with that path; homonto does not adopt, switch, or clean
that checkout to free it. Resolve that checkout explicitly before retrying.

Existing bindings for the workflow/change pin the generation across all aliases
and roles. Without a binding, receiver allocation uses matching state at the
active path (including terminal-but-active state), or one unambiguous terminal
archive when that path is absent. Multiple archived generations require
`--state-id <id>`, using the native state `id` or a registered `stateID` from
inspected evidence. For example:

```bash
homonto worktree receiver add-search --workflow onto --repo app --state-id <recorded-id> --config /work/control/homonto.toml --json
```

The selector must match the intended generation and any existing bindings; it
does not override a reused active name, rebind ownership, or change the recorded
base/target. Missing, conflicting, or ambiguous identity evidence fails closed.

Read the returned `path`. A receiver has `role: receiver`; an execution binding
has `role: execution` (older role-less bindings mean execution). Both can exist
for the same workflow/change/repo. Repeating receiver allocation validates its
binding and cleanliness. It never becomes the execution path used by source
verification or clean gates, and allocation itself does not merge, fetch, push,
or complete integration. After archive, perform integration in this checkout
under the source workflow's policy and record onto receipts as applicable.

## Records history and source commits

In managed mode, each mutating onto/to binary operation declares its exact
record paths or source/destination subtrees under a history lock and persists
**pre-write intent** before the handler runs. Its checkpoint selects changed
paths only within that operation scope, not all records that happened to change
concurrently. An ordinary state setter does not own neighboring Markdown;
archive/conversion owns its selected source and destination, and delta merging
owns the named living-spec targets. Unrelated changes, guides, and specs are
not swept into the commit. Multi-step operations use one outer checkpoint;
no normalized record change means no empty commit. Failed operations may leave
durable partial progress, which history preserves. Read-only commands do not commit.

For managed archives, the source, exact destination (including any numeric
suffix), date, and source-state fingerprint are prepared once before pre-write
intent is persisted. The handler uses that same plan rather than choosing a new
date or path after its gates run. It checks source directory/state identity
before the first write and rechecks source/destination-parent identity and target
absence before moving. A midnight rollover cannot make the journal and actual
archive use different dates; a replaced source or occupied target is refused,
not silently redirected to another archive.

Scope is path-level, not editor attribution: homonto cannot distinguish arbitrary
editors modifying the same owned file or subtree during an operation or after
a crash. A selected file is committed as a whole, so inspect and checkpoint
pre-existing edits and avoid concurrent writes within an operation's scope.

This is not an editor watcher: Markdown edits need an explicit checkpoint
before the next state mutation, phase exit, or handoff. Inspect the diff and
name only the files or subtrees that belong to the operation:

```bash
homonto workspace checkpoint --config /work/control/homonto.toml --path changes/add-search/proposal.md --message "Record search proposal"
homonto workspace checkpoint --config /work/control/homonto.toml --path tasks/tune-cache/plan.md --message "Record cache verification"
```

`--path` is repeatable and relative to `workflow.root`. Allowed trees are
`changes`, `tasks`, `specs`, `adr`, `guides`, and `.workflow`; omitting `--path`
selects all owned records, so prefer a path-scoped checkpoint. Homonto excludes
transient locks and staging files and refuses symlinked records or nested
Git metadata. Leave the managed index unstaged; checkpoints refuse pre-staged
work rather than absorbing it.

Checkpoints compare Git-normalized content, so normal CRLF conversion through
Git configuration or attributes is supported without rewriting working files.
An edit that leaves the normalized content unchanged creates no empty commit.

Source commits remain separate work in the source checkout. A records
checkpoint or archive SHA is not evidence that source was tested or integrated.
Onto records verification and integration per selected source alias, with no
extra implicit config-repo receipt. In `existing` mode, commit named records
under their Git owner's normal policy; `workspace checkpoint` requires managed
mode.

### Recover a pending checkpoint

A commit/hook failure or interrupted pre-write intent leaves edits in place and
reports pending history; later managed mutations are blocked. It does not roll
back a workflow operation. Stop further mutations, inspect the state, scoped
diff, and history, fix any Git identity or hook problem, then recover:

```bash
homonto workspace inspect --config /work/control/homonto.toml --json
homonto workspace recover --config /work/control/homonto.toml
```

Recovery distinguishes two cases:

- **Prepared checkpoint:** retry exactly the journaled content, checking
  Git-normalized content, original raw bytes, index, and base HEAD. Later edits
  are refused even if normalization would make them identical, as are changed
  normalization rules that alter the commit. An exact successful commit can be
  acknowledged after a crash before journal cleanup.
- **Interrupted pre-write intent:** compare the current scoped files to the
  stored pre-operation snapshot, checkpoint only that scoped delta under
  `Recover interrupted operation: <label>`, or clear a no-change intent without
  an empty commit. This preserves partial progress, **not proof the workflow
  operation completed**. It cannot attribute later edits within the same scope
  to the operation rather than another editor.

Unrelated staging and unexpected HEAD movement block recovery. Inspect the
workflow state afterward to decide what remains. Do not rerun a possibly
completed workflow command or delete the journal as a shortcut. Preserve later
edits before arranging an explicit repair if recovery refuses.

## Remove a finished worktree

```bash
homonto worktree remove add-search --workflow onto --repo app --yes --config /work/control/homonto.toml
homonto worktree remove add-search --workflow onto --repo app --role receiver --yes --config /work/control/homonto.toml
```

`--role` defaults to `execution`; `receiver` selects only that separate binding.
The roles can be removed independently, in either order, under the same checks.

Removal requires valid ownership, terminal workflow state, a clean checkout
including **untracked and ignored files**, and proof that its current HEAD is
an ancestor of the recorded integration target. Terminal means archived or
abandoned for onto, and `done` or `abandoned` for to. Abandonment does not waive
the ancestry check. `--yes` confirms removal; it is not a force option.

Removal also refuses `assume-unchanged` or `skip-worktree` index flags,
including those used by sparse checkouts, even when `git status` looks clean.
Inspect and preserve any hidden work before explicitly resolving that refusal.

A PR receipt alone is insufficient. The target ref must locally contain the
worktree's commits; a squash/rebase merge may not satisfy this ancestry test.
For new schema-2 records, choose that local branch with `new --base alias=branch`
before record creation; worktree creation cannot change the target afterward.
Fetch or integrate through your normal source workflow as needed. Homonto
does not do that during removal, and it preserves the source branch afterward.

Interrupted worktree operations can leave `creating` or `removing` registry
entries. `list` validates bindings and fails on incomplete or stale entries
instead of presenting them as usable. There is no worktree recovery command;
inspect and arrange manual recovery without deleting unknown work.

A retained binding reserves its change name in that workflow even after archive
or abandonment, including when the next change would select different repos.
Remove or recover the old binding before reusing the name; do not delete its
registry entry to bypass ownership checks.

## Trust and current limits

`apply` grants the builtin coordinator and implementers bounded external-path
access to declared sources and the per-alias namespaces under `worktrees.dir`,
not the entire allocation parent. The coordinator also receives access to an
external records root. Implementers do not receive that records grant, and an
explicit records deny overrides a broader source grant when records sit inside
an external source. The coordinator owns bookkeeping and checkpoints; workers
own their assigned source edits. Read-only specialists stay shell- and
edit-denied; custom agents do not inherit these grants.

These permissions are **not a sandbox**. OpenCode checks external paths
lexically, so trusted symlinks can reach outside a declared tree. Allowed
builds and scripts execute with the process's privileges, including access
to credentials and the network. Agent role instructions and command rules
do not guarantee that every host prompt or shell composition is enforced.

Configuration migration currently consists of guards, not a migration command.
Changing schema, records root, or Git mode while workflow state exists can
fail closed, including for archives and recovery directories. Managed history
also binds absolute config and records paths. Restore the prior configuration
on refusal; do not delete ownership markers or archives to bypass the guard.
There is no `homonto workspace migrate` command or ownership-safe rebind API.

`to promote` and `onto demote` refuse conversions involving registered
worktree bindings because they cannot transactionally rebind ownership to the
other workflow. Active bindings cannot be removed either, so removal is not a
conversion route for an active change. Continue in the current workflow; no
ownership-preserving active conversion command is available. Deleting a registry
entry is not a supported conversion procedure.

Exact flags and output: [homonto CLI reference](cli-reference.md).
Workflow gates: [onto reference](onto-reference.md) and [to reference](to-reference.md).
