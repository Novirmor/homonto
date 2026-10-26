# OpenCode 2

homonto targets OpenCode 2 by default since v0.32.0. This guide covers what
that means: the workflow context plugin and its terminal panel, terminal
settings, and the V2 runtime surfaces that are not implemented yet. For the
release that introduced it, see the v0.32.0 entry in
[`CHANGELOG.md`](../../CHANGELOG.md).

## Overview

The builtin
`onto`, `to`, and `h` frameworks install the V2 workflow server and terminal plugin.
For new work without an explicit workflow choice, the coordinator recommends
and asks once among viable `to`, `onto fix`, `onto tweak`, and full `onto` paths;
an existing change resumes in its recorded workflow without another prompt.
Supported V1 server configuration syntax remains accepted by OpenCode 2; a
wholesale JSON rewrite is not required. Explicitly enabling the old
`workflow_bridge` still loads a V1 plugin and is **not** V2-compatible. `plan`
and `doctor` report that opt-in and flag external plugins as unverified; these
are advisory findings, not a successful runtime compatibility test.

Generated worker record-edit restrictions use V2's path rules: paths inside the
projection's launch directory are location-relative, and external records use
absolute canonical paths. They no longer use V1's Git-worktree-relative base
(or `/` for non-Git workspaces). Mixing those bases can deny unrelated source
files in nested layouts. Worker shell/edit ownership rules are still separate
from directory access. Generated permission paths target V2; the accepted legacy
frontmatter spelling does not imply V1 runtime path compatibility.

## Read-only workflow context and panel

```toml
[integrations.opencode]
workflow_context = true
```

This declaration is needed only when no builtin workflow framework is present.
`workflow_context` defaults to true for builtin `onto`, `to`, and `h` frameworks
and false otherwise. Explicitly set it to false to opt out. The old bridge is
not installed by default. An explicitly true `workflow_bridge`, or an enabled `[plugins.opencode.<name>]` with
`source = "homonto-workflow"`, conflicts with this option: explicitly disable or
remove that declaration before applying. homonto never silently disables an
explicit plugin. The bundled `permission-observer` can be declared separately.

Apply installs the project-local relative directory link
`.opencode/plugins/homonto-workflow-context` →
`../../.homonto/catalog/plugins/homonto-workflow`, even without a framework.
Its `index.ts` is the V2 server entrypoint and `tui.tsx` is the terminal entrypoint.
An older homonto-owned standalone `.ts` context link is retired on apply.
It uses the same `binding.json` selected-config identity as the legacy bridge,
so a nested launch directory does not select a different config. Switching
between bridges removes the inactive homonto-owned link; foreign paths are
preserved and reported as conflicts. Disabling `workflow_context` does not
automatically restore the V1 bridge; that requires an explicit legacy opt-in.

Before upgrading, keep a backup of V1 config and state, install matching versions
of `homonto`, `onto`, and `to`, inspect `homonto plan`, then run `homonto apply`.
Quit and restart OpenCode after the apply. Do not point V1 OpenCode at V2-only
terminal settings; restoring a V1 setup requires its original config, not only
a homonto snapshot undo.

The V2 bridge supplies read-only workflow snapshot context for model requests
and compaction only when the session's project matches the plugin's project
and the session directory's realpath is within the bound config root. Detached
external worktrees do not receive context yet. In a V2 terminal session, open
the command palette and select **Open workflow status**; the panel displays a
fresh bounded snapshot with health findings and exact-generation recovery
pointers. Press `r` in the panel to refresh. The terminal uses its connected
server's read-only RPC; it does not read the terminal machine's local files,
which matters when the server is remote. With the builtin `h` framework, the
server registers V2 GitHub draft tools using native question forms and a
bound-service permission request; authorization fails closed if that service
cannot be verified. It also emits session-scoped terminal notifications on
idle observation. It provides no workflow mutations or verification authority.
With `h` installed, readiness reports the live-host validation requirement. The
Go workflow commands remain available; injected context is not authorization
to act or evidence that work passed verification.

## Remaining runtime work

V2 has verified alternatives to several V1 surfaces:
[local plugin discovery](https://opencode.ai/v2/docs/plugins/#discover) loads
direct `.ts` files under `.opencode/plugins/`, and the V2.0.16
[full client](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/client/src/promise/generated/client.ts)
exposes resource-specific `permission.create`. The
[injected permission context](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/plugin/src/promise/permission.ts)
exposes only `list`, `get`, `reply`, and the evaluation hook, not `create`.
The V2 GitHub tools use the documented local service registration and HTTP
API, check an instance-specific RPC
identity, and treat a matched permission reply and correlated native form answer
as separate authorization steps. Connecting to a standalone server without a
registered local service leaves those tools unavailable rather than bypassing
the permission gate. The terminal receives notifications through connected RPC.

The V2 `permission-observer` suggests only when two correlated **`once`** replies
approve the same exact single-resource shell command. The V2.0.16
[shell producer](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/core/src/tool/plugin/shell.ts)
does not attach the exact command. The observer correlates the request's tool
source with the command observed in the tool hook and requires an identical
single shell resource. The
[permission producer](https://github.com/anomalyco/opencode/blob/v2.0.16/packages/core/src/permission.ts)
emits indistinguishable `always` replies for some automatic approvals, so these
do not count toward the threshold. Execution is not approval. Before the stable
release, the owner reported completing live V2 and real GitHub sandbox checks.
In an isolated V2.0.16 server,
the read-only panel and permission → native question → shell permission →
receipt flow were exercised with a local model and a fake `gh` binary; denial
never sent, and allowing both approvals sent once to the fake destination.
That isolated run made no external GitHub publication or model call; the
separate real-GitHub sandbox did exercise publication in a throwaway repo.

The next V2-native opportunities are not implemented yet:

- **Publication transaction:** consolidate the existing draft/approval tools
  into one independently verified V2 preview → approve → publish operation;
  until then the bound-service permission and native question must both succeed.
- **Worktree-aware sessions:** associate a session with an explicitly selected
  workflow generation and declared checkout. Extend the current containment
  guard only with verified bindings, and invalidate context when sessions move.
- **Watcher notifications:** extend the current session-idle notifications to
  debounced filesystem changes without losing headless observation.

## Terminal settings

Terminal settings now target **OpenCode V2 only**. Keep the `[tui.opencode]`
TOML surface; it projects to global `$XDG_CONFIG_HOME/opencode/cli.json`, or
`~/.config/opencode/cli.json` when XDG_CONFIG_HOME is unset. There is no
project-local terminal settings projection.

XDG support here is limited to the terminal file; existing server configuration
and resource projection still use `~/.config/opencode`.

```toml
[tui.opencode]
theme = { name = "gruvbox", mode = "system" }
scroll = { speed = 3, acceleration = false }
tabs = { mode = "auto" }

[tui.opencode.keybinds]
"app.exit" = ["ctrl+c", "<leader>q"]
"help.show" = false
```

Supported native groups follow the [V2 CLI reference](https://opencode.ai/v2/docs/cli/config/):
theme, cursor, scroll, leader, prompt, session, tabs, diffs, attention, terminal,
mini, keybinds, debug, and experimental, plus animations, mouse, and `$schema`.
Keybinding command IDs containing dots must be quoted in TOML.
Unknown fields and invalid values fail before projection. Terminal plugin
declarations are outside this projection and are rejected.

Four legacy forms have direct mappings: string `theme` → `theme.name`,
`scroll_speed` → `scroll.speed`, `scroll_acceleration.enabled` →
`scroll.acceleration`, and `leader_timeout` → `leader.timeout`. Declaring both
an alias and its native destination is an error. Other V1-only settings, including
`diff_style`, `attention.enabled`, and old keybind IDs such as `app_exit`, require
an explicit native V2 replacement; they are never copied blindly.

Only declared settings are migrated. Unmanaged `cli.json` values, including
nested sibling settings and plugins, survive apply and prune. Global and
project V1 `tui.json(c)` files remain untouched. If you want OpenCode to migrate
other V1 preferences automatically, start V2 before homonto creates `cli.json`.

Old `tui.*` state records are retired without deleting V1 file content; new
records use `tui.cli.*` and manage individual native settings. Restoring old
state requires another V2 plan, and replaying historical V1 write changes is
rejected rather than redirected into `cli.json`. The existing snapshot engine
re-plans current TOML and restores checkpoint state: **snapshot undo does not
reverse this file migration**. To restore prior terminal values, declare them
and apply again. Supported V1 `opencode.json(c)` settings,
`mcp` entries, and `plugin` arrays keep their existing format. Native `plugins`
in `[settings.opencode]` is reserved to prevent overriding managed plugin
declarations. Runtime plugin migration is separate. See the
[V1 migration guide](https://opencode.ai/v2/docs/migrate-v1/).
