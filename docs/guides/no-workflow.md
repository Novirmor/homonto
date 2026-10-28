# The no engine

`no` is the smallest engine: one implementer, one reviewer, and a
coordination contract. No binary, no phases, no lifecycle records. The model
chooses how to break down and verify the work.

Use it when you want an implement-and-review loop and nothing written to
`docs/`. Comparison: [workflows](workflows.md).

## Install

```toml
[engines.no]
source = "builtin:no"
scope  = "project"

[subagents.no-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.no-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
```

This installs the `no` skill, the `/no` command, and the two agents. It does
**not** install the `homonto` coordinator; `/no` runs in your current agent.
Install `onto`, `to`, or `h` alongside to get the coordinator as well.

## Use

```text
/no rename the --verbose flag to --debug across the CLI and its docs
```

Or, through GitHub: `/h-resolve-issue acme/cli#88`, then choose `no` when the
coordinator asks which workflow fits.

## The flow

```text
 request ─▶ inspect checkouts & dirty work ─▶ (ask once: existing checkout or worktree?)
         ─▶ implementer (one per repository) ─▶ parent reviews evidence, commits
         ─▶ no-reviewer on each repository's candidate ─▶ combined review / integration checks
         ─▶ deliver to the requested endpoint
```

1. **Scope.** The parent reads the request, the declared roots, and every
   affected checkout. Dirty paths are shown; you choose preserve, isolate, or a
   specific cleanup once.
2. **Isolation.** For a new task, you choose once between working in the
   existing checkouts and creating worktrees (per-repository exceptions
   allowed).
3. **Implement.** `no-implementer` gets one writable repository, its absolute
   checkout, the base, the task, and the verification to run. It returns edits
   and evidence; the parent commits. Independent repositories run in
   parallel; one writer per checkout.
4. **Review.** `no-reviewer` reviews each repository's candidate (no writers
   active there), then the parent reviews cross-repository interfaces.
5. **Integration checks.** Configured `[integration.*]` checks run when their
   repositories changed; a configured failure can't be replaced by an ad-hoc
   check.
6. **Deliver.** Stop at your endpoint: a commit, a pushed branch, a PR
   (publication needs the caller's authority, e.g. `h-resolve-issue`).

## Shared task bookkeeping (schema 2)

With `schema_version = 2` and declared `[repos]`, the parent records the task
so it survives a restart — this is checkout bookkeeping, not a lifecycle:

```sh
homonto workspace task create rename-flag --engine no --repo cli --repo docs --isolation worktree
homonto workspace task inspect rename-flag
homonto workspace task check rename-flag --writers-paused
homonto workspace task cleanup rename-flag --yes     # after integration
```

Data lives under `.homonto/workspace-tasks/rename-flag/`. See
[workspaces](workspaces.md#workspace-tasks).

Without schema 2, the parent keeps the checkout choice and evidence in the
session and handoff instead.

## What no never does

- invoke `onto` or `to`, or create their records;
- stash, reset, or commit your work as setup;
- continue past a configured check failure;
- publish without the caller's authority.

## The agents

| Agent | Can | Cannot |
|---|---|---|
| `no-implementer` | Edit files, run trusted shell (builds, tests, scripts) in its assigned repository | Push, `gh` publishing, run `onto`/`to`/`homonto`, delegate, ask you |
| `no-reviewer` | Read files and the web | Edit, run shell, delegate, ask you |
