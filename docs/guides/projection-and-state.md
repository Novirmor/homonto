# Projection & state — how apply works

The mechanics behind `plan`/`apply`/`status`: the surgical merge, symlinked
content, state, drift vs. pending, adoption, and pruning.

## The pipeline

`homonto.toml` parses into one tool-agnostic desired-state model. Each tool
(OpenCode, the only adapter since v0.13.0) is an **adapter** that runs
`Read → Plan → Apply`:

1. **Read** the tool's current files.
2. **Plan** the diff against the desired state (`+` create, `~` update,
   `-` delete).
3. **Apply** — after confirmation and up-front secret resolution — writing
   each file **atomically** (temp + rename), so an interrupted run never
   leaves a half-written file.

State persists after each successful adapter, so a failure in the second tool
never loses the first tool's applied records.

## Surgical merge

homonto writes **only the keys it manages** and preserves every unmanaged key
in the tool's file. Consequences:

- Your hand-added MCP servers, settings, and plugins survive every apply.
- An **unparseable** tool file makes that adapter abort and report; homonto
  never overwrites a file it cannot understand.
- Adapters write a file **only when a managed key inside it changes**. A
  skills-only apply leaves tool JSON files byte for byte untouched, which is
  also why OpenCode JSONC comments survive link-only applies but not applies
  that rewrite `opencode.jsonc`.

## Owned content is symlinked

Skills you author live under `homonto/skills/` (the local provider root, next
to `homonto.toml`) and are **symlinked** into each tool, so editing the
source is instantly live everywhere. `apply` ensures the links exist and
point correctly. It **never clobbers** a real file or a symlink pointing
elsewhere; those are reported as conflicts.

Link-mode commands and subagents work the same way; copy-mode subagents are
projected as real managed files instead (see [subagents](subagents.md)).

> Symlinks store an **absolute** target. If you move or rename the repo,
> existing links point at the old path and are reported as conflicts. Delete
> the stale links and re-run `apply` to relink (see
> [troubleshooting](troubleshooting.md)).

## State — `.homonto/state.json`

State lives next to your config and records the last-applied snapshot per
resource: the unresolved desired value plus a sha256 hash of the applied
value (never a plaintext secret — see [secrets](secrets.md)). It also records
the versions behind each apply (binary, catalog, per-framework), which is how
`homonto update` prints its version transition and the workflow doctors
detect binary/framework skew.

State additionally records the **catalog version** and a **subagent render
fingerprint**, a digest of the per-agent models (and per-tool target sets)
the projected agents were stamped from. Builtin content re-materializes only
when one of those inputs moved (or a file it would write is missing), so a
settled workspace stays a true no-op while a changed
`[subagents.<name>.<tool>]` block re-renders the agent that reads it.

Because a catalog file is reached through a name-based symlink, re-rendering
it changes no *projected* value and so produces an empty plan. `apply` runs
anyway in that case and reports
`No projection changes; catalog re-materialized.` — the same carve-out
`remote:` sources get, for the same reason.

State is generated; the scaffolded `.gitignore` excludes `.homonto/` by
default.

## `status`: drift vs. pending

`homonto status` compares the values homonto manages on disk against the
state snapshot and reports two **independent** things:

- **Drift** — a managed key changed on disk *outside homonto* since the last
  apply, or was deleted:

  ```
  opencode setting.theme drifted (will reset on apply)
  opencode mcp.foo missing (deleted out of band)
  ```

- **Pending** — edits to `homonto.toml` not yet applied. The disk still
  matches the last apply, so this is not drift; it is a count:

  ```
  1 config change(s) awaiting apply (run `homonto apply`)
  ```

When neither is present, `status` prints `No drift.` The distinction
matters: editing your config should not look like something changed your
files behind your back. Use `homonto plan` to see exactly what a pending
apply would write.

For scripting, `status --exit-code` exits `2` on pending and `3` on drift,
and `--output json` emits the report structurally.

## Adoption: pre-existing resources are recorded quietly

When a declared resource already exists on disk with **exactly** the value
homonto would write — an MCP server you added by hand, or config
bootstrapped by `import` — `apply` **adopts** it: the resource is recorded
into state with no file write, no diff line, and no prompt.

- `plan` stays silent about adoption. It prints `No changes` when adoption
  is the only outstanding work, because nothing will be *written*.
- `apply` performs the adoption even when it is the only work, without a
  confirmation prompt (only `state.json` is touched), and reports it:
  `Reconciled 1 pre-existing resource(s) into state.`

Why it matters: an adopted resource becomes fully managed, visible to drift
detection and, if you later remove it from `homonto.toml`, to pruning.
Before adoption, a hand-created resource could look managed while silently
escaping both.

Adoption never touches secret-bearing values (those are always re-applied so
the resolved value is verified), and an apply whose only effect is adoption
leaves `opencode.jsonc` byte for byte unchanged, comments included.

## Pruning: removal is declarative too

Remove a resource from `homonto.toml` and the next `apply` deletes it from
the tool files (and removes owned-content links). Only resources homonto
recorded in state are ever pruned; your own hand-added keys stay untouched.

## A typical loop

```console
$ homonto status
1 config change(s) awaiting apply (run `homonto apply`)   # you edited homonto.toml

$ homonto plan                                            # see the diff
opencode:
  ~ setting.model: "old" -> "new"

$ homonto apply                                           # write it
...
Applied.

$ homonto status
No drift.                                                 # disk matches state
```
