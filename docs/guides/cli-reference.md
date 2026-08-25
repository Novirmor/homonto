# CLI reference

Every command Homonto has. The surface is pinned by a test, so this list is
complete rather than representative.

`--workspace <path>` is a persistent flag on every command; without it,
Homonto uses the working directory.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | The command did what it was asked |
| `1` | The command failed |
| `2` | A guard **refused** a presented operation |
| `3` | `doctor` found something that needs attention |

`2` is distinct from `1` on purpose: a host hook needs to tell the guard
working from the guard breaking.

## Workspace

### `homonto init`

Create a workspace here.

| Flag | Meaning |
|---|---|
| `--workflow` | `task` (default) or `change` |
| `--member <path>` | A repository to include. Repeatable |
| `--discover` | Propose members and write nothing |

Refuses a directory that is already a workspace. "Initialize again" is not
something a workspace survives: the runtime database, the checkpoint, and
the manifest describe each other.

### `homonto status [--json]`

The workspace, its work, and its host integrations.

### `homonto doctor [--json]`

Check members, host integrations, active work, and whether a self-update
was interrupted. Reports; never repairs. Exits `3` when something is wrong.

### `homonto version`

### `homonto handoff [name-or-id]`

Make the work portable: mark its checkpoint transferable, commit it,
release this machine's leases. See [recovery](recovery.md).

### `homonto attach`

Pick up work handed off from another machine.

| Flag | Meaning |
|---|---|
| `--propose` | Show the proposed member locations and stop |
| `--member <id>=<path>` | Confirm one member's location. Repeatable |
| `--force` | Take over a checkpoint another machine already consumed |

## Task

Available when the workspace's workflow is `task`.

### `homonto task start <name> [--goal <text>]`
### `homonto task status [name-or-id] [--json]`

Reconciles before reporting, so what you see is where the task actually is.

### `homonto task abandon [name-or-id]`

Stops the task. Isolation areas, branches, and evidence are left in place.

## Change

Available when the workspace's workflow is `change`.

### `homonto change start <name> --request <text>`

Opens a **classification candidate**, not a change. Nothing is written
under `docs/homonto/changes/` until you confirm the path.

### `homonto change status [name-or-id] [--json]`
### `homonto change abandon [name-or-id]`

Abandoning an unconfirmed candidate removes nothing, because nothing was
created.

## Protocol

What a host tool speaks. See [protocol](protocol.md).

### `homonto next [name-or-id] [--json]`

What to do now. Safe to repeat: an outstanding group comes back unchanged.

### `homonto report [--file <path>]`

A role report, as protocol JSON on stdin or from a file. For a writable
assignment, what actually changed on disk is validated **before** anything
is recorded.

### `homonto decide`

| Flag | Meaning |
|---|---|
| `--action`, `--token` | Which decision |
| `--choice` | The chosen value |
| `--rationale` | Required when the choice says so |
| `--answer` | For question gates only |
| `--file` | Read the submission as JSON instead |

An empty choice is refused: silence is not approval.

### `homonto accept-edit --action <id> --token <grant-token>`

Finish a document edit. Homonto looks up what the grant opened rather than
believing a structure you hand back.

### `homonto guard [--file <path>] [--action …] [--grant …]`

Decide a presented write, reading a `GuardRequest` on stdin. Exits `2` on a
refusal, having written the decision to stdout.

## Host

### `homonto host install`

| Flag | Meaning |
|---|---|
| `--tool` | `claude` or `opencode`. Default: the ones in use here |
| `--adopt` | Replace generated files you edited |
| `--commit` | Commit the generated files instead of ignoring them |
| `--dry-run` | Show what would change and write nothing |
| `--binary` | How the wrappers invoke Homonto |

### `homonto host probe --host <claude\|opencode>`

The read-only resume probe. Writes nothing, migrates nothing, and reaches
no network. A directory that is not a workspace is **answered**, not
refused — a host runs this everywhere.

### `homonto host guard --host <claude\|opencode>`

The write hook. Reads that host's own event shape on stdin and answers in
its own response shape.

Assignment and grant credentials arrive through the environment:
`HOMONTO_ACTION_ID`, `HOMONTO_ACTION_TOKEN`, `HOMONTO_GRANT_ID`,
`HOMONTO_GRANT_TOKEN`.

## Update

### `homonto update trust`

Which signing roots this build accepts a release from. A build carrying
none cannot update itself.

### `homonto update candidate-metadata` (hidden)

What this binary is: version, protocol, store schema, trust roots. It
exists for one binary to interrogate another and answers with no network
access and no workspace.
