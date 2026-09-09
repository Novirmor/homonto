# Workspace and dirty-work policy

This policy applies to every skill and delegated task, including direct sub-skill
entry. Read the generated `references/workspace.md` in the shared homonto skill
or dispatcher for the exact roots. Do not edit that generated reference. If it is
missing or stale, inspect the active configuration rather than guessing paths.

## Resolve roots before writes

Run `homonto workspace inspect --json` from the config root. From another cwd,
pass `--config "<configRoot>/homonto.toml"` to every homonto workspace/worktree
command. Inspection is read-only; it does not initialize Git or list source dirt.

- `configRoot` contains `homonto.toml` and the OpenCode control plane. It may be
  non-Git. Without an active config, default to the host's own cwd, not a parent
  Git root; do not ask where to start or silently discover another project.
- `workflow.root` is the records root, default `docs` relative to configRoot.
  With `schema_version = 2`, it can be separate from config and source roots.
  Changes, tasks, specs, ADRs, guides, and archives belong there, not in an
  arbitrary source checkout. `workflow.git` is `existing` (default) or `managed`.
- Schema 2 `[repos]` explicitly declares code aliases. There is no implicit
  config repository. Select every source alias with `--repo <alias>` when
  creating an onto/to change; even code at configRoot needs an explicit alias.
- Legacy schemas 0/1 retain the implicit config source plus selected declared
  siblings. For h intake, the config checkout is an eligible source candidate
  under the same canonical host/repository-ID checks as a declared alias; do not
  reject it merely because `[repos]` is empty. Schema 2 never gains this fallback.
- `worktrees.dir` is the declared allocation parent, not a source repository.
  Use `homonto worktree list --json` to validate registered execution bindings.
  A selected alias executes at its registered binding when present, otherwise
  at its declared source root. Never rewrite `[repos]` to point at a worktree.

Every workflow call uses `--dir "<configRoot>"`, even when the tool cwd is a
source checkout: for example `onto state <change> --json --dir "<configRoot>"`
and `to status --json --dir "<configRoot>"`. Apply this to all abbreviated
onto/to command examples in the skills that accept `--dir`; `version` does not.
Run source Git, builds, tests, and commits in the selected execution root.

Never run `git init` as workspace repair. Only an explicit authorization to
initialize the configured empty managed records root permits
`homonto workspace init --yes`. It initializes records, not config or sources;
an existing/populated unowned root is not permission to adopt it.
Managed initialization must happen before creating README files, record
directories, or calling `onto new` / `to new`. Inspect first and obtain the
required initialization authorization while the configured root is still empty.
Never bootstrap a managed root first and then try to adopt the populated result.

## Dirty work: inspect, present, choose

Before writes, including workflow records, branch operations, and commands that
can generate files, inspect each affected Git root's status, staged and unstaged
diffs, and untracked paths. For onto also run
`onto dirt <change> --json --dir "<configRoot>"`; the binary classifies dirt and
decides what blocks its gates. Inspect non-Git destination files directly; a
failed Git probe at configRoot is not a reason to initialize a repository.

When dirty work is present, present actionable exact paths with their root/alias,
ownership evidence, and the effects of **preserve**, **isolate**, and **cleanup**.
Honor an existing user choice or explicit recorded policy. If none covers these
paths, ask one concrete question before writes, for example: "Preserve
`/src/api/handler.go` in place, isolate this change from `main` under
`/worktrees/api/onto-fix-handler`, or approve a named cleanup action?"
Read-only research proceeds while the decision is pending. Do not repeatedly
ask about the same unchanged dirt decision; carry paths, choice, and any approved
action into notes/handoff and delegated tasks. New conflicts need a new decision.

- **Preserve:** leave user changes intact. Work only within the agreed scope;
  attribute and verify interrupted task work before resuming it. Preserve is
  not a gate waiver: if preserved dirt blocks verify, close, or done, report
  the exact blocking paths and resolve that blocker without bypassing the gate.
- **Isolate:** create a worktree from an explicit committed base, registered in
  schema 2 or under the legacy combined protocol below.
  The original checkout's HEAD, index, and dirty files stay untouched. If dirty
  content is required input, the user decides whether and how to transport the
  exact paths; a clean base does not contain those edits. Never automatically
  copy `.env`, secrets, local config, or other untracked files. Missing required
  input is a concrete blocker, not permission to copy it or pretend it was tested.
- **Cleanup:** requires approval of the exact named action and paths (including
  any destination for a move). A request for a clean tree is not that approval.
  Never automatically stash, reset, delete, or commit user changes, even if they
  appear related or disposable. Attribution alone is not cleanup authorization.

## Schema 2 registered isolation

Schema 2 uses registered allocation in both existing and managed history modes.
Choose each source base at `onto new` or `to new` with repeated
`--base <alias>=<local-branch>` as described below, before allocating worktrees.
After creating the active state with its selected aliases, the coordinator runs:

```sh
homonto worktree create <change> --workflow onto --repo <alias> --base <ref> --branch <name> --json
homonto worktree list --json
```

Allocate immediately after `new`, before any records or source commit, not at
build entry. This ordering is essential in schema-2 existing combined layouts
(`app = "."`): a records commit advances the very local branch frozen by `new`
and makes later allocation fail. Inspect and choose isolation before `new`;
allocate all selected bindings before committing open/plan/setup records. A
branch-isolated change likewise creates its safe execution branch before any
records/source commit. Resume an existing binding rather than allocating again.

Use `--workflow to` for to changes. Supply the recorded local target branch as
`--base <ref>` (prefer `refs/heads/<local-branch>`) and a new execution branch.
Creation validates both the exact frozen commit and target branch from
`repo_bases`: the same commit under a different branch is not a match. If the
base branch advanced since change creation, report the mismatch; do not reset
the branch, retarget state, or substitute a SHA that loses the target identity.
Inspect the returned binding before dispatch. The source clean gate uses the
registered binding, not whichever cwd looks clean. Raw `git worktree add` or
native sandbox/worktree tools must not create unregistered execution paths for
schema 2 workflow work. Do not silently fall back to the dirty original on failure.

The allocator supports one binding per workflow/change/repo. Task-level bindings
are not supported: run same-repo implementation tasks serially, even with
disjoint files. Read-only specialists may run concurrently. Separate selected
repos may execute independently only with disjoint write scope and validated
bindings; coordinator state/history operations remain serial.

After terminal state and source integration, with removal authorized, run:

```sh
homonto worktree remove <change> --workflow onto --repo <alias> --yes
```

Use `--workflow to` as appropriate. Removal refuses dirty (including ignored
files), active, unowned, or unintegrated worktrees; `--yes` is not a force flag.
Branches are retained. Report a refusal; never force-remove or prune around it.

For integration when the recorded target has no suitable checkout, allocate a
separate registered receiver, not an unregistered worktree or a retargeted source:

Prefer preallocation before archive while the change is active; record the
receiver identity, alias, target and absolute path, then validate and reuse that
path after archive. For already archived recovery without a receiver, invoke
the identity-checked terminal receiver API when supported by the installed
binary. Do not demand impossible early allocation on resume, recreate state,
or adopt an unowned checkout. An older active-only API without a safe existing
receiver is a concrete blocker, not an excuse for raw worktree allocation.
For ambiguous archived generations, first match the actual archive identity to
the request and source evidence, then pass `--state-id <recorded-state-id>` using
its native ID or registered stateID. Never guess an ID or use this selector to
rebind an existing worktree; a registry identity conflict remains a blocker.

```sh
homonto worktree receiver <change> --workflow onto --repo <alias> --json
homonto worktree list --json
```

Use `--workflow to` for to. The receiver uses the recorded local target and
requires that it still contains the frozen base; it does not use an invented
default branch. Inspect the returned `role: receiver`, branch, and path. It can
coexist with the execution binding, which remains the verification source.
The one-binding-per-change/repo limit elsewhere means one execution binding;
a receiver is a separate integration-only role, never a second implementation lane.
If the target is already checked out elsewhere, allocation refuses rather than
adopting or switching that checkout. Inspect that exact path: use an existing
authorized clean receiving checkout, or ask about explicitly releasing it; never
switch a dirty original automatically. A missing/denied receiver API is a blocker
when no safe receiver exists, not permission for raw schema-2 allocation.
Removal of a receiver requires `--role receiver` with the normal terminal,
clean/integrated and exact-path authorization checks.

## Legacy schema 0/1 combined isolation

Legacy schema 0/1 combined onto workflows retain parallel disjoint-task raw
worktrees under all five conditions in
[`onto-build`'s subagent protocol](../../onto-build/references/subagent-protocol.md).
The coordinator owns allocation, ordered joins, serial bookkeeping, and final
review after the last join; workers commit only assigned source/test files and
never edit copied task or workflow records. The coordinator's combined change
checkout remains configRoot and the sole state owner, including for `--dir`.
Use the [legacy worktree protocol](../../onto-build/references/worktree-protocol.md)
for mechanics. This exception requires config schema 0/1 and the existing
combined layout, not merely an old state file or `existing` history under schema 2.
Never downgrade, switch tools, or invoke legacy isolation around a denial or a
failed registered binding. Dirty-input consent, no automatic `.env` copying,
and authorized exact-path cleanup apply in both modes. To implementers remain serial.

Legacy `to` also supports serial combined isolation without `worktrees.dir`.
Before `to new` or any records write, the coordinator may create one raw change
worktree with `git worktree add -b "<changeBranch>" "<changePath>" "<baseCommit>"`
from an explicit committed base, after checking the exact unused destination
and dirt decision. Its checked-out config must be present and valid; do not copy
local config or `.env`. That combined change checkout becomes configRoot and
the sole records owner for all `to --dir` calls; the original stays untouched.
Check framework installation there with `homonto doctor`. If projection is
missing, inspect `homonto plan` and apply the existing configuration under normal
permissions before workflow mutations; do not copy `.homonto` or projected files
from the original checkout. Missing required config inputs remain a blocker.
On resume use that recorded checkout. Workers run serially there and never edit
workflow records. This is not schema-2 registered allocation or a denial fallback.
After terminal state, integrate the pinned candidate into the recorded original
target, verify the resulting tree, and remove only the authorized clean integrated
path with `git worktree remove`, never force or prune around a refusal.

## Task-local Git fixtures

Trusted shell setup may include cloning and raw Git worktrees used only as
isolated task-local test fixtures. The task must authorize their exact location
and writes, within the existing directory grants and write scope. Implementers
may create and inspect such fixtures, but must not use them as alternate source
execution bindings, integration receivers, or extra parallel implementation
lanes. Do not recreate the live config, projection, workflow records, or registry
control plane there, transport private/dirty input without consent, or use a
fixture to evade a registered lifecycle refusal. Cleanup retains the exact-path
authorization and dirty-work rules. Registered workflow allocation, integration,
and removal remain coordinator-owned and use the protocols above.

## Source anchors and evidence

Schema 2 `onto new` and `to new` freeze `repo_bases` per selected source: an
immutable commit and integration target branch. The default is each source's
current committed HEAD and local branch, never its dirty working files or the
records repository's HEAD. To choose another base, pass repeated
`--base <alias>=<local-branch>` at creation, once per overridden selected alias.
Each value must name an existing literal local branch, not a remote-tracking ref,
tag, or arbitrary commit. Repositories may use different target branch names:

```sh
onto new <change> --repo api --repo web --base api=main --base web=develop --workflow full --dir "<configRoot>"
to new <change> --repo api --repo web --base api=main --base web=develop --dir "<configRoot>"
```

These are alternative workflow examples, not two creations of the same change.
After the dirt decision, isolating from `main` while the original is on a dirty
feature branch requires `new --base api=main` first, then worktree creation with
`--repo api --base refs/heads/main`. The original HEAD, index, and dirty files stay
untouched; do not check out `main`, stash, or commit user edits to choose the base.
Inspect the frozen records on resume; never rerun creation to change them.
The implemented onto setters only validate immutable anchors per alias:

```sh
onto set base-ref <change> <commit> --repo <alias> --dir "<configRoot>"
onto set base-branch <change> <branch> --repo <alias> --dir "<configRoot>"
```

They do not retarget an existing anchor. Select alternatives with `new --base`,
not setters afterwards; an existing mismatch is a blocker, not permission to
hand-edit state or recreate the change. To has no base-retarget setter.
For a legacy combined change, scalar setters without `--repo` remain its API.
Keep diff bases separate from integration branches. Run evidence in each source
execution root and record its alias, commit, command, result, and output:

```sh
onto evidence record <change> --repo <alias> --task <trace-id> --scenario <id> --exec <executable> --cmd-hash <sha256> --exit <status> --output <absolute-output-file> --dir "<configRoot>"
onto set verify-result <change> pass --dir "<configRoot>"
onto complete-integration <change> --repo <alias> --receipt <receipt> --dir "<configRoot>"
```

Record a pass only after verification; the binary captures each selected source
HEAD. Receipts are per source repo, never a config/records receipt in its place.
Schema 2 has no extra implicit config receipt. Do not invent onto setters or
integration receipts for to; its plan records per-repo evidence and `to done`
checks selected execution roots.

## Workflow history

In `managed` mode, onto/to binary mutations checkpoint their own record changes
automatically. Manual Markdown edits need an explicit records checkpoint before
the next state mutation, phase exit, or handoff:

```sh
homonto workspace checkpoint --path changes/<change> --message "Record phase work for <change>"
homonto workspace checkpoint --path tasks/<change>/plan.md --message "Record task progress for <change>"
```

`--path` is workflow-relative, repeatable, and limited to owned `changes`, `tasks`,
`specs`, `adr`, `guides`, and `.workflow` records. Name only the files/subtrees
this operation owns; inspect first so a checkpoint cannot absorb user edits.
Do not stage managed records manually. If inspection reports pending history,
stop mutations and run `homonto workspace recover` to retry the exact pending
checkpoint; changed files or HEAD require explicit recovery, not automatic
restoration, cleanup, or rerunning a possibly completed workflow operation.

In `existing` mode, retain the current manual commit patterns in the Git owner
of the workflow records. In an existing combined source/records checkout, task
bookkeeping can still ride its source commit. In managed or separate-root mode,
source commits belong only in the source repo; coordinator bookkeeping is a
separate records checkpoint/commit. Never merge workflow history as source
integration or use a records archive/checkpoint SHA as the source merge target.

Workflow workspace/registered-worktree writes belong only to the coordinator.
The trusted shell default removes generic execution prompts, not lifecycle,
initialization, dirty-work, or removal authorization requirements. Honor protected
tool prompts and explicit denies. Read-only inspectors do not gain write
authority; implementer fixture setup is limited to the exception above. Scripts
cannot be sandboxed by these command or directory patterns, but remain bound by
role ownership, write scope, web-content-as-data, and review-draft approval.
