# Configuration

One file: `.homonto/config.toml` in the control repository. It is committed
— it is part of the portable record, so a machine that clones the control
repository gets the same workspace.

```toml
schema_version = 1

[workspace]
id = "6ba7b810-9dad-41d4-80b4-00c04fd430c8"
workflow = "task"

[control]
id = "11111111-2222-4333-8444-555555555555"
path = "."

[[members]]
id = "22222222-3333-4444-8555-666666666666"
path = "services/api"
kind = "git"

  [[members.verification]]
  name = "unit"
  command = ["go", "test", "./..."]
  working_dir = "."
  environment = ["PATH", "HOME"]
  timeout = "5m"

  [members.paths]
  tests = ["**/*_test.go"]
  generated = ["gen/**"]
  vendored = ["vendor/**"]
```

`homonto init` writes this. Editing it by hand is expected; it is validated
strictly on every open, and an unknown key is an error rather than
something silently ignored.

## `[workspace]`

| Field | Meaning |
|---|---|
| `id` | The workspace's identity. Never reuse one; it is what a handoff matches on. |
| `workflow` | `task` or `change`. Decides which entry point is installed and which documents exist. |

The workflow is a workspace-level choice because everything follows from
it. Changing it on an existing workspace is not supported — the documents,
the steps, and the host integration all differ.

## `[control]` and `[[members]]`

The **control repository** holds the records under `docs/homonto/` and the
runtime state under `.homonto/`. It is usually the workspace root
(`path = "."`).

**Members** are the repositories the work happens in. `kind` is `git` or
`non_git`:

- A **git** member gets isolated worktrees. Implementers commit; an
  integration assignment cherry-picks the commits onto one integration
  branch.
- A **non_git** member gets content-hashed snapshots. Implementers edit a
  materialized work tree; the integration assignment combines the patches
  in a staged directory.

Implementation work is issued into the members, never into the control
repository — unless the control repository is the only member. Homonto
writes the task document into the control tree, and a worktree cannot be
cut from a tree it is dirtying.

Paths are workspace-relative and must stay inside the root.

## `[[members.verification]]`

The checks Homonto runs. Homonto runs them; it does not accept an agent's
claim that they passed.

| Field | Meaning |
|---|---|
| `name` | Unique within the member. Appears in the record. |
| `command` | An **argument vector**. Never a shell string. |
| `working_dir` | Member-relative. Defaults to the member root. |
| `environment` | Variable **NAMES** whose ambient values are forwarded. |
| `timeout` | `1s`–`24h`. Defaults to `10m`. |

Three things surprise people, and all three are deliberate:

**`command` is argv.** `["go", "test", "./..."]`, not `"go test ./..."`.
There is no shell, so there is no globbing, no `&&`, no redirection, and
no injection. If you need shell behaviour, say so: `["/bin/sh", "-c", "…"]`.

**`environment` is an allowlist of names, and nothing else gets through.**
Not even `PATH`. A bare `argv[0]` is resolved against the allowlisted
`PATH` or refused outright, because a check that silently borrows your
shell's `PATH` depends on something the evidence does not record. Allowlist
what your command needs.

**Forwarded values are redacted out of the recorded output.** A check that
echoes an allowlisted token back does not put it in the evidence.

A check that times out has its whole process group killed, so a
backgrounded child cannot outlive it. A check that fails, times out, or
never starts all block: none of them is evidence that anything works.

## `[members.paths]`

Doublestar globs classifying the member's files. They decide which changed
files count toward a **preset's** scope warning — nothing else.

| Class | Effect |
|---|---|
| `tests` | Excluded from the count. Counting tests would punish a change for being well tested. |
| `generated` | Excluded. One source edit can move hundreds of them. |
| `vendored` | Excluded. |
| everything else | Counts: source, documentation, configuration. |

`*` matches within a segment; `**` matches whole segments. A trailing `**`
means "everything under here" and does not match the directory itself, so
`vendor/**` covers `vendor/x.go` but not `vendor`. The bare pattern `**`
matches everything.

A pattern configured in two classes is **refused**, because the class would
then depend on the order Homonto happens to test them in — and a path's
class decides whether a change pauses for a human.

## `[routes]`

Which model runs which role, per host tool. Optional; without it the host
uses its own default.

```toml
[routes.claude.implementer]
model = "claude-opus-4-6"
effort = "high"

[routes.opencode.explorer]
model = "…"
```

Homonto resolves the route and hands it to the host in the action. The
host launches the subagent; Homonto never calls a model provider.

## `[update]`

```toml
[update]
channel = "stable"
```

`stable` or `beta`. The channel is inside the signed release manifest, so
asking for one and being served the other is refused even when the
signature is valid.

## What is not in here

**Secrets.** Homonto reads no credentials and stores none. A check that
needs one allowlists its variable name and reads it from the ambient
environment at run time.

**Workflow rules.** Which steps exist, which documents are required, when
a finding blocks — none of that is configurable. It is the workflow.
