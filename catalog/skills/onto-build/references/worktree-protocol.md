# Worktree isolation by config mode

Follow the shared [workspace and dirty-work policy](../../homonto/references/workspace-policy.md).
`onto set isolation <name> worktree --dir "<configRoot>"` records the choice,
not a binding. Inspect dirt, present exact paths, and honor the user's existing
preserve/isolate/cleanup decision before writes; ask once if none covers it.

Inspect the config schema and actual layout before choosing a protocol. The
legacy exception below requires schema 0/1 and an existing combined workflow;
`workflow.git: existing` or an old state file alone does not enable it in schema 2.
In both modes the coordinator alone owns workflow state, task records, workflow
allocation, and integration. Workers commit only assigned source/test files. Never copy
`.env` or untracked input automatically; the user decides required dirty-input
transport. Honor tool permissions; no fallback around a denial or failed binding.

## Schema 2 registered allocation

Create active state with selected source aliases first. For an alternative base,
pass `--base <alias>=<local-branch>` to `onto new` before allocation, preserving
the dirty original checkout. With `worktrees.dir` declared, the coordinator
allocates from that frozen base:

Do this immediately after `onto new`, before any records/source commit, not at
build entry. In an existing combined schema-2 layout, a records commit advances
the frozen local target and would make allocation fail. Resume validated bindings.

```sh
homonto worktree create <name> --workflow onto --repo <alias> --base <ref> --branch <name> --json
homonto worktree list --json
```

Here `--base <ref>` names the recorded local target, preferably
`refs/heads/<local-branch>`. Both the exact frozen commit and target must match;
a different branch at the same commit or a target advanced since creation is a
mismatch. Do not retarget setters, reset refs, or replace the target with a SHA.

Run from configRoot or pass `--config "<configRoot>/homonto.toml"`. Use the
validated binding path for source commands and implementer dispatch, while every
workflow call keeps `--dir "<configRoot>"`. No unregistered raw/native workflow
execution worktrees are permitted in schema 2. Isolated task-local Git fixtures
are allowed only under the shared workspace policy's fixture exception; they
cannot become execution bindings or recreate the live control plane.
No permission-error fallback to the dirty original, no config alias rewrites,
and no automatic `.env` or
untracked-file copying. If dirty content is required input, the user decides
its transport before setup; do not claim the committed baseline includes it.

Run the project's relevant baseline checks in that source worktree. The allocator
supports one binding per workflow/change/repo. Run same-repo tasks serially until
task-level bindings exist. Source commits stay there; workflow records stay at workflow.root.
Integrate the verified source branch per onto-close, not the records history.

After terminal state and source integration, with removal authorized:

```sh
homonto worktree remove <name> --workflow onto --repo <alias> --yes
```

Removal keeps the branch and refuses dirty, unowned, active, or unintegrated
worktrees. Report failures without forcing removal or pruning around the registry.

## Legacy schema 0/1 combined parallel

The existing combined onto workflow retains raw task worktrees for parallel
disjoint-file tasks only under all five conditions in `subagent-protocol.md`.
Never downgrade configuration or use this path to evade a denial or registered
allocation failure. Schema 2 must use the registered protocol above.

The coordinator keeps one authoritative combined change checkout as configRoot.
Inspect that checkout and each planned path before writes; verify the task branch
and destination are unused. From the coordinator checkout, create each task tree
from an explicit committed change-branch base:

```sh
git worktree add -b "<taskBranch>" "<taskPath>" "<baseCommit>"
git worktree list
```

Record each exact task path, branch, base, and assigned file set before dispatch.
Run baseline checks there without copying `.env` or untracked input automatically.
The coordinator owns creation/removal; implementers never allocate their own
workflow execution trees or edit the copied `tasks.md`, `plan.md`, or workflow
state. Every workflow call retains `--dir "<configRoot>"`, never a task worktree
as its state owner.

Verify returned source commits, then join them into the coordinator's change
branch in plan order. Perform every checkoff and bookkeeping commit serially
after the joins; run final review only after the last join. Verify the integrated
candidate before continuing to workflow verification and close. Partial joins
are recorded and resumed, never reset or replayed blindly.

After the task commits are integrated, remove only the exact owned task tree,
with removal authorized and its status clean including untracked/ignored files:

```sh
git worktree remove "<taskPath>"
```

Keep branches unless their deletion is separately authorized. Never force-remove,
prune around a refusal, clean user work automatically, or fall back to another
tool after a denial. The change's main isolation tree stays until close/integration.
