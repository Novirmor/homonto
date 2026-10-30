# Getting started

This walkthrough takes an empty repository to a working OpenCode setup with a
builtin skill, a setting, and both workflow engines — alongside an MCP server
and a skill of your own — then runs one change through each engine. Every output below is real, with long paths
shortened to `…`.

You need `homonto`, `onto`, and `to` installed ([installation](installation.md))
and a Git repository to work in. OpenCode itself is only needed to *use* what
homonto projects.

> All three binaries print to **stderr**. Use `2>&1` when piping.

## 1. Scaffold

```console
$ cd ~/work/notes
$ homonto init
created homonto.toml
created .gitignore
created .env.example
```

`init` never overwrites a file, never runs `git init`, and installs nothing. It
writes:

- `homonto.toml` — every section commented out, as a menu.
- `.gitignore` — ignores `/.homonto/` (projection state) and `.env`.
- `.env.example` — a place to document `${ENV_VAR}` secrets.

`homonto init ~/dotfiles/ai` scaffolds another directory.

## 2. Bring your own MCP servers and skills

homonto doesn't manage MCP servers or skills you write yourself. Put them where
OpenCode looks, and homonto leaves them alone:

```console
$ cat > opencode.jsonc <<'EOF'
{
  "mcp": {
    "codegraph": {"type": "local", "command": ["codegraph", "serve", "--mcp"], "enabled": true}
  }
}
EOF
$ mkdir -p .opencode/skills/house-style
$ cat > .opencode/skills/house-style/SKILL.md <<'EOF'
---
name: house-style
description: Team writing and naming conventions for this repo.
---

# House style
EOF
```

## 3. Declare what homonto manages

Replace `homonto.toml` with a builtin skill and a setting:

```toml
[skills.handoff]
source = "builtin:handoff"
scope = "project"

[settings.opencode]
model = "anthropic/claude-opus-5-5"
```

`plan` is a dry run. It writes nothing and never resolves a secret:

```console
$ homonto plan
opencode:
  + setting.model = "anthropic/claude-opus-5-5"
  + skill.handoff = …/notes/.opencode/skills/handoff -> ../../.homonto/catalog/skills/handoff
```

`+` creates, `~` updates, `-` deletes, `>` releases something homonto used to
manage and leaves it in place for you.

## 4. Apply

```console
$ homonto apply
opencode:
  + setting.model = "anthropic/claude-opus-5-5"
  + skill.handoff = …
Apply these changes? [y/N] y
Applied.
```

`apply` resolves every secret **before** writing anything — one missing value
aborts with nothing touched. Then it writes each file atomically and touches
only keys it manages. The result:

```text
notes/
├── homonto.toml
├── opencode.jsonc                           ← yours; never rewritten
├── .opencode/skills/house-style/SKILL.md    ← yours
├── .opencode/skills/handoff -> ../../.homonto/catalog/skills/handoff
└── .homonto/state.json                      ← hashes, never secret values
```

The setting lands in your global `~/.config/opencode/opencode.jsonc`.

## 5. Check health

```console
$ homonto status
No drift.

$ homonto doctor
warn: `pass` not found on PATH (pass: references will fail)
ok: .config/opencode (OpenCode) config location present
ok: skill "handoff" linked (opencode)
```

`status` separates two things. Edit `homonto.toml` and you get **pending**:

```console
$ homonto status
1 config change(s) awaiting apply (run `homonto apply`)
```

Change a managed value behind homonto's back and you get **drift**:

```console
$ homonto status
opencode setting.model drifted (will reset on apply)
```

`homonto status --exit-code` exits `2` for pending and `3` for drift.

## 6. Install the workflow engines

Add both engines. Each one installs skills, slash commands, and agents, and
**every installed agent needs a model** — homonto has no defaults:

```toml
[engines.onto]
source = "builtin:onto"
scope = "project"

[engines.to]
source = "builtin:to"
scope = "project"

# The shared coordinator (installed by onto, to, and h — declare once).
[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"

# onto specialists
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
variant = "high"

# to specialists
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

Use model IDs your OpenCode providers offer. Forget one and the config fails
to load, naming it:

```console
$ homonto plan
Error: config "…/notes/homonto.toml": parse config: subagents.homonto.opencode model is required
```

Plan and apply again. The plan now lists the shared `homonto` skill and
coordinator, the `onto-*` and `to-*` skills, commands, and specialists:

```console
$ homonto apply --yes
opencode:
  + command.onto = …/.opencode/command/onto.md -> ../../.homonto/catalog/commands/onto.md
  …
  + skill.homonto = …/.opencode/skills/homonto -> ../../.homonto/catalog/skills/homonto
  …
  + subagent.onto-skeptic = …/.opencode/agent/onto-skeptic.md -> ../../.homonto/catalog/subagents/onto-skeptic.md
  …
Applied.
```

Commit the projection so teammates get it too (`.homonto/` stays ignored;
they run `homonto apply` themselves):

```console
$ git add -A && git commit -m "Install homonto workflows"
```

## 7. Use it from OpenCode

Start OpenCode in the repository and type a slash command. Every workflow
command runs in the `homonto` coordinator agent:

```text
/onto add full-text search to the notes CLI
/to cap the note cache at 100 entries
/onto-light search crashes on an empty query
```

For new work with no explicit path, the coordinator asks **once** which
workflow fits (`to`, `onto light`, or full `onto`) and recommends
one. After that it drives the change to its endpoint — implementation,
review, verification, archive, integration — and stops only for a real
decision, an endpoint you named, or a hard blocker. See
[workflows](workflows.md).

The rest of this page shows what the coordinator does under the hood with the
binaries, so you can follow along or run it by hand.

## 8. An onto change by hand

```console
$ onto init
created docs/changes
created docs/specs
created docs/adr
created docs/guides

$ onto new add-search
created change "add-search" at …/docs/changes/add-search
  …/docs/changes/add-search/onto-state.yaml
  …/docs/changes/add-search/proposal.md

$ onto gate add-search
[Proposal] Has the proposal been reviewed against the request and grounding?
  → onto set proposal-approved add-search "<evidence>"

$ onto advance add-search
error: onto advance: cannot leave open: proposal review not recorded (review the proposal, then run `onto set proposal-approved add-search "<evidence>"`)
```

The binary refuses until the phase's evidence exists. Write the proposal,
record its review, and advance:

```console
$ $EDITOR docs/changes/add-search/proposal.md
$ onto set proposal-approved add-search "proposal matches the request; scope excludes ranking"
add-search: updated
$ onto advance add-search
add-search: open → design
```

Design produces `design.md`, delta specs under `specs/`, and `tasks.md`. Build
also needs an isolation choice:

```console
$ onto set approach-confirmed add-search "linear scan chosen over index: <1k notes"
$ onto set isolation add-search branch
$ onto advance add-search
add-search: design → build
```

Build writes `plan.md`, implements each task with its own commit, and checks
it off in `tasks.md`. Unchecked tasks block the exit:

```console
$ onto advance add-search
error: onto advance: cannot leave build: tasks.md has unchecked items
```

Verify records evidence and a result; close merges specs, archives, and
records the integration. The [onto workflow](onto-workflow.md) walks the whole
flow with every file's contents. The end looks like this:

```console
$ onto merge-deltas add-search
  merged search → docs/specs/search.md
add-search: 1 delta spec(s) merged; marked close.merged
$ onto close add-search
add-search: archived to docs/changes/archive/2026-10-27-add-search; integration pending
$ git switch main && git merge --no-ff onto/add-search
$ onto complete-integration add-search --receipt merge:$(git rev-parse HEAD)
add-search: integration complete for repository config (merge:b7e101e…)
$ onto doctor
healthy
```

## 9. A to change by hand

`to` is three phases and one artifact:

```console
$ to init
created docs/tasks
created docs/tasks/archive

$ to new tune-cache
created change "tune-cache" at docs/tasks/tune-cache
  docs/tasks/tune-cache/to-state.yaml
  docs/tasks/tune-cache/plan.md

$ $EDITOR docs/tasks/tune-cache/plan.md       # goal, boundary, task contracts
$ to phase tune-cache
change "tune-cache" advanced plan → do

# … implement, review, commit each task …

$ to done tune-cache
error: to done: --verified is required; verify the change first (the to-done skill), then assert it
$ to done tune-cache --verified --evidence "go test ./... ok; to-skeptic: no blocking findings"
change "tune-cache" done, archived at docs/tasks/archive/2026-10-27-tune-cache
```

`--verified` is a recorded assertion, not a check. The `to-done` skill is
where the real verification happens. See the [to workflow](to-workflow.md).

## Where next

| You want to… | Read |
|---|---|
| Understand the moving parts | [Concepts](concepts.md) |
| Copy a config for your situation | [Examples](examples.md) |
| Look up a `homonto.toml` field | [Configuration](configuration.md) |
| Choose between onto, to, no, and h | [Workflows](workflows.md) |
| Know what each skill and agent does | [Catalog](catalog.md) |
| Coordinate several repositories | [Workspaces](workspaces.md) |
