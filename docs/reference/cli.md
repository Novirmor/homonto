# CLI Reference

`--workspace <path>` is available on every command. Without it, Homonto uses
the current directory as the workspace root.

## Exit Codes

| Code | Meaning |
|---|---|
| `0` | Command completed. |
| `1` | Command failed. |
| `2` | A guard refused a presented operation. |
| `3` | `doctor` found a condition that needs attention. |

## Workspace Commands

| Command | Purpose |
|---|---|
| `homonto init` | Initialize a Task or Change workspace. `--discover` lists members without writing; `--member` is repeatable. |
| `homonto status` | Show workspace, work, and host-integration state. |
| `homonto doctor` | Report member, integration, active-work, and interrupted-update conditions. A normal run opens the workspace read-write and can perform recovery. |
| `homonto version` | Print binary version information without opening a workspace. |
| `homonto handoff [name-or-id]` | Commit a portable checkpoint and release local leases for active work. |
| `homonto attach` | Attach a portable checkpoint. `--propose` shows member mappings, `--member <id>=<path>` confirms them, and `--force` takes over consumed work. |
| `homonto update trust` | Report signing roots carried by the binary. It does not fetch or install an update. |

`homonto update candidate-metadata` is a hidden binary-to-binary support
command. It is not part of the user interface.

## Workflow Commands

| Command | Purpose |
|---|---|
| `homonto task start <name> [--goal <text>]` | Start a Task workspace record. |
| `homonto task status [name-or-id] [--json]` | Reconcile and show Task state. |
| `homonto task abandon [name-or-id]` | Stop Task orchestration without deleting its work. |
| `homonto change start <name> --request <text>` | Create a local classification candidate. |
| `homonto change status [name-or-id] [--json]` | Reconcile and show Change state. |
| `homonto change abandon [name-or-id]` | Abandon a Change or candidate. |

Task commands require a Task workspace. Change commands require a Change
workspace.

## Protocol Commands

| Command | Purpose |
|---|---|
| `homonto next [name-or-id] --json` | Return the current action or action group. |
| `homonto report [--file <path>]` | Submit a role report from stdin or a file. |
| `homonto decide` | Submit a human decision using `--action`, `--token`, `--choice`, and any required rationale or answer. |
| `homonto accept-edit --action <id> --token <grant-token>` | Accept a document edit performed under an issued grant. |
| `homonto guard` | Evaluate a protocol `GuardRequest` from stdin or `--file`; exits `2` when it refuses. |

`guard` accepts assignment credentials through `--action` and `--token`, and
edit-grant credentials through `--grant` and `--grant-token`. See the [host
protocol reference](host-protocol.md) for payloads and host behavior.

## Host Commands

| Command | Purpose |
|---|---|
| `homonto host install` | Install a Claude Code or OpenCode integration. `--tool`, `--adopt`, `--commit`, `--dry-run`, and `--binary` control the installation. |
| `homonto host probe --host <claude\|opencode>` | Return a read-only resume result for a host session. |
| `homonto host guard --host <claude\|opencode>` | Translate a host hook event into a guard decision. |

See [Install a host](../how-to/install-a-host.md) for operational use and
[host integration](host-integration.md) for generated-file behavior.

## Environment

| Variable | Purpose |
|---|---|
| `HOMONTO_ACTION_ID`, `HOMONTO_ACTION_TOKEN` | Assignment credentials used by host guards. |
| `HOMONTO_GRANT_ID`, `HOMONTO_GRANT_TOKEN` | Edit-grant credentials used by host guards. |
| `HOMONTO_STATE_ROOT` | Absolute root for non-Git registration and lease state. |
