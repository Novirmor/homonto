# OpenCode integration

homonto targets **OpenCode 2**. This page covers the bundled workflow plugin,
the GitHub draft tools, terminal settings, upgrading from OpenCode 1, and the
limits of observing a workflow from inside the tool.

## The workflow plugin

Declaring `builtin:onto`, `builtin:to`, or `builtin:h` installs the
`homonto-workflow` plugin as a project-local link:

```text
.opencode/plugins/homonto-workflow-context -> ../../.homonto/catalog/plugins/homonto-workflow
```

Its `index.ts` is the server entrypoint and `tui.tsx` the terminal one. It
finds the config through its own binding metadata, so starting OpenCode in a
subdirectory still uses the right `homonto.toml`.

What it provides — all **read-only** toward workflow state:

| Feature | What you see |
|---|---|
| Model context | Bounded workflow snapshot (phase, tasks, pending decisions) injected into model requests and compaction, for sessions inside the bound config root. Up to three in-flight changes; 16 KiB budget; 1.5 s deadline; truncation is stated explicitly. |
| Status panel | Command palette → **Open workflow status**: a fresh snapshot with health findings and recovery pointers. Press `r` to refresh. |
| Coordinator tools | `homonto_status` (the snapshot) and `homonto_handoff` (exact-generation recovery artifacts), coordinator only. |
| Notifications | Session-scoped notices on idle observation. |
| GitHub draft tools | With `h` only — [below](#github-draft-tools). |

It never advances a phase, records evidence, writes state, runs `doctor`, or
blocks a session. Injected context is data, not authorization or proof that
anything passed.

Control it with `[integrations.opencode]`:

```toml
[integrations.opencode]
workflow_context = false   # opt out (default true with onto/to/h)
```

```toml
# No builtin workflow package, but you still want the panel and context:
[integrations.opencode]
workflow_context = true
```

Sessions in detached external worktrees don't receive context yet. The
terminal reads the snapshot through its connected server, not local files,
which matters when the server is remote.

### The data behind it

The plugin reads the same JSON you can read yourself:

```console
$ homonto workflow snapshot --json
$ homonto workflow handoff --workflow onto --change add-search --identity <id> --json
```

`snapshot` lists onto and to changes with identities, phase, checklist
progress, pending decisions and integration, and findings for malformed
state. A missing workflow is empty; unreadable state is a finding, not "done".
`handoff` returns config and records roots, validated execution directories,
decisions, bounded artifact excerpts (64 KiB per file, 12 KiB combined), and
the next skill. Neither writes anything.

## GitHub draft tools

With `[workflows.h]`, the plugin registers:

| Tool | Does |
|---|---|
| `homonto_github_draft` | Stage an immutable, memory-only batch: issue comments, PR comments, or commit-bound `COMMENT` / `REQUEST_CHANGES` reviews. |
| `homonto_github_status` | Show statuses, the exact full preview, and the native question arguments. |
| `homonto_github_publish` | Publish only items approved through the matching native question replies. |

The flow: stage → show the full preview → ask with OpenCode's native question
dialog (passing the returned arguments unchanged) → publish approved items by
draft ID. Declining or revising publishes nothing. A permission reply alone,
or a model-supplied "approved" flag, never counts.

Limits: ≤10 items per batch, 8 KiB per body, 32 KiB per batch, 15-minute
lifetime, lost on restart. Restaging invalidates earlier approvals. Uncertain
sends are reconciled rather than resent. Headless clients without native
questions cannot approve. The tools don't push or create PRs. Shipped worker
agents are denied them.

On OpenCode 2 the tools also require a bound local service and verify its
identity; if it can't be found they fail closed. `homonto plan` warns:

```text
warn: OpenCode V2 GitHub publication requires a bound local service, native question approval, and a live host verification; fail closed if the matching service cannot be discovered
```

## Permission observer

```toml
[plugins.opencode.permission-observer]
source = "permission-observer"
```

Watches shell permission prompts in memory. When the same exact single
command is explicitly approved twice in a session (OpenCode 2: two `once`
replies; automatic `always` replies don't count), it suggests the line to
add via `homonto permissions suggest`. A rejection disqualifies the command
for that session. Execution alone is never treated as approval. It never
edits configuration.

## Other plugins

```toml
[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"
```

Third-party plugins are listed in OpenCode's `plugin` array. homonto can't
know whether one works on OpenCode 2, so plan warns:

```text
warn: OpenCode V2 compatibility unverified for plugin "@slkiser/opencode-quota": verify its V2 entrypoint and runtime behavior before cutover; config normalization does not port plugins
```

## Terminal settings

`[tui.opencode]` projects into the V2 terminal file
`$XDG_CONFIG_HOME/opencode/cli.json` (default `~/.config/opencode/cli.json`).
Only declared keys are managed; everything else in the file survives.

```toml
[tui.opencode]
theme  = { name = "catppuccin", mode = "dark" }
scroll = { speed = 5, acceleration = true }
leader = { timeout = 1500 }
tabs   = { mode = "auto" }

[tui.opencode.keybinds]
"app.exit"     = ["ctrl+c", "<leader>q"]
"help.show"    = false
```

Plan keys look like `tui.cli.theme.name`. Allowed groups and the four
mapped V1 spellings are in [configuration](configuration.md#terminal-settings--tuiopencode).
V1 `tui.json` files are never touched. To have OpenCode migrate other V1
preferences itself, start OpenCode 2 before homonto first creates `cli.json`.
`homonto snapshot undo` does not reverse this migration; declare old values
and apply again to restore them.

## Upgrading from OpenCode 1

1. Back up your OpenCode config and `.homonto/`.
2. Install matching `homonto`, `onto`, and `to` versions.
3. `homonto plan` — review terminal-setting moves and plugin warnings.
4. `homonto apply`, then quit and restart OpenCode.

Supported V1 `opencode.json(c)` settings, `mcp` entries, and `plugin` arrays
keep their format. The legacy V1 bridge (`workflow_bridge = true`) still
exists but is **not** OpenCode 2 compatible and conflicts with
`workflow_context`. Restoring a V1 setup needs your original config, not just
a snapshot undo.

Generated permission paths follow V2 rules: paths inside the launch
directory are relative to it, external ones absolute. V1's Git-root-relative
base isn't emitted (it would deny unrelated paths in nested layouts).

## Gates vs. observers

The `onto` binary's gates are hard **when it's invoked** — nothing forces an
inattentive agent to invoke it. The plugin makes progress and problems
visible; it does not make the workflow non-skippable.

For scripted diagnostics, both binaries have a quiet doctor:

```sh
onto doctor --quiet --dir "$ROOT"; echo $?   # 0 healthy, non-zero on findings
to doctor --quiet --dir "$ROOT"; echo $?
```

`onto doctor` findings include missing layout, malformed state, a phase whose
artifacts are missing, unresolved dependencies, archived-but-active changes,
`tasks.md`/`plan.md` drift, stale or unknown evidence, duplicate scenario IDs,
three or more failed verify rounds while still failing, and binary/catalog
version skew.

A custom OpenCode plugin can run it on idle. This one only logs:

```ts
import type { Plugin } from "@opencode-ai/plugin"

// Runs `onto doctor --quiet` when a session goes idle; non-zero means a
// workflow integrity finding. Read-only; it never mutates state and does
// not block session completion.
export const OntoGuard: Plugin = async ({ $, directory }) => ({
  event: async ({ event }) => {
    if (event.type === "session.idle") {
      const result = await $`onto doctor --quiet --dir ${directory}`.nothrow()
      if (result.exitCode !== 0) {
        console.error(`onto doctor: workflow integrity finding (exit ${result.exitCode})`)
      }
    }
  },
})
```

Throwing from the handler is not a documented way to block completion. A
blocking policy needs a host-supported interception point; don't infer
enforcement from a toast or a log line.

## Not implemented yet

- One independently verified V2 preview → approve → publish transaction
  (today: bound-service permission plus native question).
- Worktree-aware sessions bound to a specific workflow generation and
  checkout.
- Filesystem-watcher notifications (today: idle notifications).
