# Getting started

This takes one task from an empty directory to an archived record. It
assumes Git and either Claude Code or OpenCode.

## Install

```bash
go install github.com/noviopenworks/homonto@latest
homonto version
```

A binary you build yourself carries no signing root, so `homonto update`
is unavailable in it. That is deliberate: a build that could replace
itself from the network without a trusted signature would be worse than
one that cannot update at all. `homonto update trust` tells you which you
have.

## Initialize a workspace

A workspace is a **control repository** — which holds the records — plus
the **members** the work actually happens in. Often the control repository
is the only member; often it is a directory holding several.

```bash
mkdir demo && cd demo
homonto init --discover
```

`--discover` proposes members and writes nothing. A scan cannot know which
directories are part of your work, so it never adds one for you:

```
candidates under /home/you/demo (confirm with --member):
  git      /home/you/demo/services/api
  non_git  /home/you/demo/assets
```

Confirm the ones you want:

```bash
homonto init --workflow task --member services/api --member assets
```

That writes `.homonto/config.toml`, creates the control repository, and
scaffolds `docs/homonto/`. Choose the workflow now: it decides which
command is installed and which documents exist, and a workspace runs one.

- `--workflow task` — `plan → do → done`, one file per task.
- `--workflow change` — `open → design → build → verify → close`, with Fix
  and Tweak presets.

## Tell Homonto how to verify

Homonto runs your checks itself. Add them to `.homonto/config.toml`, per
member:

```toml
[[members]]
id = "…"
path = "services/api"
kind = "git"

  [[members.verification]]
  name = "unit"
  command = ["go", "test", "./..."]
  environment = ["PATH", "HOME"]
  timeout = "5m"
```

`command` is an argument vector, never a shell string. `environment` is an
allowlist of variable NAMES whose ambient values are forwarded — nothing
else reaches the command, so a check that calls `grep` must allowlist
`PATH`. This is stricter than you may expect, and it is the point: a check
that silently depends on your shell is a check whose evidence does not
describe what ran.

## Install the host integration

```bash
homonto host install
```

This installs one command, one skill, a read-only resume probe, and one
write hook for each host tool it finds in use. The generated files are
project-local and gitignored; `--commit` opts into committing them.

## Run a task

```bash
homonto task start fix-login --goal "Login fails after a restart."
```

Then, in your host tool, run `/homonto-task`. The agent asks Homonto what
to do and does exactly that. If you want to watch from the terminal:

```bash
homonto next --json
```

The response has a `state`:

- `ready` — every action in `actions` may run now, in parallel.
- `blocked` — one decision, and it is yours. Homonto shows the choices and
  which need a rationale. It does not pick.
- `complete` — nothing left to do.

A task walks: parallel explorers survey the members → you write the goal
and checklist under an edit grant → a skeptic attacks the plan → any
consequential question it raised is put to you → implementers work in
isolated worktrees → an integration assignment combines them → Homonto
runs your checks → a reviewer and a skeptic assess the result → the record
is archived.

## What you get

```
docs/homonto/tasks/archive/2026-08-25-fix-login.md
```

Checked off, with the exact commands and their outcomes, the integrated
source fingerprints, and any accepted deviations. The integration branch
is left in the member repository, ready:

```bash
cd services/api
git branch --list 'homonto/integration/*'
```

Homonto never merges it. What happens to that branch is yours.

## Where to go next

- [Task workflow](task-workflow.md) — every step and what gates it
- [Change workflow](change-workflow.md) — when a task is not enough
- [Troubleshooting](troubleshooting.md) — when something refuses
