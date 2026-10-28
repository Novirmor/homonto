# Projection and state

How `plan` and `apply` turn `homonto.toml` into files, and how homonto keeps
track of what it wrote.

## The pipeline

```text
homonto.toml ─▶ desired state ─▶ adapter: Read ─▶ Plan (diff) ─▶ confirm ─▶ resolve secrets ─▶ Apply ─▶ state
```

1. **Load** — parse and validate the whole config; any error stops here.
2. **Read** — the adapter reads OpenCode's current files.
3. **Plan** — compute `+` create, `~` update, `-` delete.
4. **Confirm** — `[y/N]` unless `--yes`.
5. **Resolve** — every `${…}` secret, before any write. One failure aborts.
6. **Apply** — write each file atomically (temp file + rename).
7. **Record** — save state.

One apply lock covers the whole plan, including every declared repository.

## What lands where

| Resource | Project scope | User scope |
|---|---|---|
| MCP server | `<root>/opencode.jsonc` → `mcp.<name>` | `~/.config/opencode/opencode.jsonc` |
| Setting | — | `~/.config/opencode/opencode.jsonc` |
| Plugin | — | `opencode.jsonc` → `plugin` array |
| Terminal setting | — | `~/.config/opencode/cli.json` |
| Skill | `.opencode/skills/<name>` → link | `~/.config/opencode/skills/<name>` |
| Command | `.opencode/command/<name>.md` → link | `~/.config/opencode/command/<name>.md` |
| Agent | `.opencode/agent/<name>.md` → link or copy | `~/.config/opencode/agent/<name>.md` |
| Workflow plugin | `.opencode/plugins/homonto-workflow-context` → link | — |
| Catalog content | `.homonto/catalog/` | — |
| State | `.homonto/state.json` (+ `state.<alias>.json` per declared repo) | — |

Plan keys name the kind: `mcp.`, `projmcp.`, `setting.`, `projsetting.`,
`plugin.`, `tui.cli.`, `skill.`, `command.`, `subagent.`.

## Surgical merge

homonto writes **only keys it manages**:

- your hand-added MCP servers, settings, and plugins survive every apply;
- a file is rewritten only when a managed key inside it changes — a
  skills-only apply leaves `opencode.jsonc` byte-for-byte untouched;
- an unparseable file makes that adapter stop and report; homonto never
  overwrites what it can't read.

**JSONC comments:** when an apply does rewrite `opencode.jsonc`, comments in
it are lost. No-op and link-only applies keep them.

## Links, not copies

Skills, commands, and link-mode agents are symlinks to their source
(`homonto/…` for `local:`, `.homonto/catalog/…` for `builtin:`). Editing the
source is live. Apply never replaces a real file or a symlink pointing
elsewhere; it reports a **conflict**. Move the file aside and re-apply.

Project links use **relative** targets, so moving the repository keeps them
valid. User-scope and cross-repository links are absolute. After a move, run
`homonto plan` and `homonto apply` from the new location; eligible stale
links whose on-disk target matches the recorded one are repaired through the
normal confirm step.

## State

`.homonto/state.json` records, per resource, the unresolved desired value and
a sha256 hash of what was written — never a secret value. It also records:

- the binary, catalog, and package versions behind each apply (used by
  `homonto update` and by `onto doctor`/`to doctor` skew checks);
- a fingerprint of the agent model blocks, so changing a model re-renders
  exactly the agents that use it;
- an operation history used by `homonto explain`.

`.homonto/` is gitignored by `homonto init`; each machine applies for itself.

When only catalog content changed (for example after `homonto update` or a
model change), plan shows no projection changes and apply reports:

```text
No projection changes; catalog re-materialized.
```

## Drift vs. pending

```console
$ homonto status
1 config change(s) awaiting apply (run `homonto apply`)    ← pending: you edited homonto.toml
$ homonto status
opencode setting.model drifted (will reset on apply)      ← drift: a file changed outside homonto
opencode mcp.foo missing (deleted out of band)
$ homonto status
No drift.
```

`--exit-code` makes scripts easy: `0` clean, `2` pending, `3` drift.
Declared-repository findings are labelled `opencode@<alias>`.

## Adoption

If a declared resource already exists on disk **exactly** as homonto would
write it — an MCP server you added by hand — apply records it in state
without writing:

```text
Reconciled 1 pre-existing resource(s) into state.
```

`plan` shows `No changes` in that case, and adoption-only applies don't
prompt. Values containing secret references are never adopted; they're always
re-applied so the resolved value is checked.

## Pruning

Remove a resource from `homonto.toml` and the next apply removes it from
OpenCode. Only resources recorded in state are pruned — hand-made keys never
are. An adopted resource is managed, so removing its declaration prunes it.

## Snapshots: undoable applies

```console
$ homonto apply --snapshot
…
$ homonto snapshot list
$ homonto snapshot undo <apply-id>       # reverse a committed snapshot apply
$ homonto snapshot recover <apply-id>    # roll back an interrupted one
```

`undo` restores state, links, copies, and structured keys, and refuses with
no changes if any managed value was edited since. `recover` finishes an
interrupted snapshot apply by rolling it back. The latest 10 committed
journals are kept; `doctor` names incomplete ones with the exact recover
command.

## Explaining a resource

```console
$ homonto explain skill house-style
skill         house-style          direct scope=project source=local:house-style
    dst: …/notes/.opencode/skills/house-style
    last: create (18dc732e, declare at 2026-10-27T17:37:54Z)
```

Kinds: `skill`, `command`, `subagent`, `subagentcopy`, `mcp`, `projmcp`,
`setting`, `projsetting`, `tui`, `plugin`. `--repo` picks a repository
partition, `--json` for machines. Values are never printed.

## Updating catalog content

```console
$ homonto update
```

Re-materializes the catalog embedded in the running binary and re-projects
it, printing the version transition, through the same plan → confirm → apply
flow. It does not download binaries — install those first (see
[installation](installation.md#upgrade)).
