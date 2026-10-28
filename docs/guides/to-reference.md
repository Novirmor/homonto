# to reference

Every `to` command and its behavior. Concepts: [to workflow](to-workflow.md).

## Conventions

- `--dir <config-root>` (default `.`) on every workspace command. Use the
  config root even from a source worktree.
- `--json` on `init`, `new`, `phase`, `done`, `abandon`, `status`, `handoff`.
- Changes live in `<workflow-root>/tasks/<name>/`; archives in
  `tasks/archive/<YYYY-MM-DD>-<name>/` (numeric suffix on same-day reuse).
- Names: lowercase alphanumeric segments joined by single hyphens
  (`fix-login`). `archive` is reserved. Active names are unique across to and
  onto.
- Output goes to stderr.

## The install gate

`init`, `new`, `phase`, `bypass`, `done`, and `abandon` refuse until:

1. `homonto.toml` exists in `--dir` (`homonto init`),
2. it declares `[engines.to]`, and
3. `homonto apply` has created `.homonto/catalog/skills/to`.

`promote` accepts an applied `[engines.to]` or `[engines.onto]`. Read-only
commands need no install.

## Commands

| Command | Does |
|---|---|
| `to init` | Create `tasks/` and `tasks/archive/`. Never overwrites. |
| `to new <name> [--repo a]… [--base a=branch]…` | Create a change at `plan` with an empty `plan.md`. |
| `to phase <name>` | The only forward transition: `plan → do`. |
| `to done <name> --verified [--evidence "…"]` | Mark done and archive. Requires phase `do`. |
| `to abandon <name>` | Terminal exit from any phase; archives. |
| `to bypass <name> --to plan\|do\|done\|archive --reason "…"` | User-requested emergency skip, audited. |
| `to promote <name> [--as n] --yes` | Convert into an onto change. |
| `to status [--all]` | Active changes; `--all` adds onto's. |
| `to handoff <name> [--write]` | Recovery pack. |
| `to doctor [--quiet]` | Workspace health. |
| `to cleanup list\|preview\|apply` | Remove clean, integrated worktrees of finished or abandoned changes ([below](#to-cleanup)). |
| `to version` | Release version. |

### `to new`

```console
$ to new tune-cache
created change "tune-cache" at docs/tasks/tune-cache
  docs/tasks/tune-cache/to-state.yaml
  docs/tasks/tune-cache/plan.md
```

- `--repo` (repeatable) selects declared source repositories. Schema 2
  requires at least one and has no implicit config source.
- `--base alias=branch` (schema 2 only, once per selected alias) anchors the
  integration target at that local branch's current tip without switching the
  checkout. Default: each source's current branch and HEAD.

```console
$ to new tune-cache --repo api --repo web --base api=main --base web=develop --dir /work/control
```

An active change or a retained worktree binding with the same name blocks
creation.

### `to phase`

```console
$ to phase tune-cache
change "tune-cache" advanced plan → do
$ to phase tune-cache
error: to phase: change "tune-cache" is already at do; finish it with `to done tune-cache --verified` or exit with `to abandon tune-cache`
```

### `to done`

```console
$ to done tune-cache
error: to done: --verified is required; verify the change first (the to-done skill), then assert it
$ to done tune-cache --verified --evidence "go test ./... ok; to-skeptic: no blocking findings"
change "tune-cache" done, archived at docs/tasks/archive/2026-10-27-tune-cache
```

`--verified` and `--evidence` are recorded verbatim and not checked. For
changes with selected repositories, every selected execution checkout must be
clean and Git-readable (see [source cleanliness](#source-cleanliness)).

The archived state:

```yaml
schema_version: 1
id: bc1e0f82
change: tune-cache
phase: done
created: "2026-10-27"
finished: "2026-10-27"
verified: true
evidence: 'go test ./... ok; to-skeptic: no blocking findings'
repo_mode: legacy
```

### `to bypass`

Skips phase, verification, and worktree gates; still needs the install
gate, valid state, and a working archive filesystem. `done`/`archive` set
`verified: false`. Every use appends the command, source, target, reason,
timestamp, and skipped checks to `.to/bypass.json`.

### `to status`

```console
$ to status
tune-cache	plan
$ to status --json
[
  {
    "change": "tune-cache",
    "phase": "plan",
    "created": "2026-10-27"
  }
]
$ to status --all
onto:
fix-crash	build
$ to status --all --json
{
  "onto": [ { "change": "fix-crash", "phase": "build", "created": "2026-10-27" } ],
  "tasks": []
}
```

Corrupt state and source-scope errors are reported per entry. An unreadable
or unsafe root fails the command instead of looking empty.

### `to handoff`

Identity, phase, the safe next skill, and a plan excerpt: the head, every
unchecked task contract, `Final Verify:`, and bounded notes/verification. A
missing `plan.md` is reported. `--json` adds the recovery envelope (schema,
operation id, derived phase, repositories, artifact digests, next argv).
`--write` persists a metadata-only pack under `.to/handoff/`.

### `to doctor`

Reports, without blocking anything: invalid state files, terminal-but-active
changes (an interrupted archive), missing `plan.md`, `do`-phase tasks missing
`Files:`/`Change:`/`Verify:`, a missing or empty `Final Verify:`,
non-terminal archive entries, binary/catalog version skew, and unavailable
selected repositories. `--quiet` reports only by exit status.

```console
$ to doctor
healthy
```

### `to promote`

Moves the whole to workspace into
`<workflow-root>/changes/<name>/.workflow/snapshots/` and starts a fresh
proposal-only onto change at `open` (promotion claims no design or
verification). It prints next steps: declare `[engines.onto]` if needed,
`homonto apply`, then `/onto`. Crash recovery is receipt-verified; tampered
staging is refused; `onto demote` immediately after with no edits restores
the original byte-for-byte. Changes with registered worktree bindings are
refused.

### `to cleanup`

```text
to cleanup list    [--json] [--dir <root>]
to cleanup preview <name> [--identity <id>] [--json] [--dir <root>]
to cleanup apply   <name> --identity <id> --yes [--json] [--dir <root>]
```

The same contract as `onto cleanup`. `to done` and `to abandon` already
archive the record, so for `to` this mostly removes registered worktrees that
are clean and integrated. Branches and archives stay. An active change is
`ineligible`:

```console
$ to cleanup list
to/add-flag	ineligible	identity=d7a6bb36	tasks/add-flag
  blocked: cleanup: to change is active; resume it or explicitly abandon it first
$ to abandon add-flag
change "add-flag" abandoned, archived at docs/tasks/archive/2026-10-27-add-flag
$ to cleanup list
to/add-flag	cleanup-complete	identity=d7a6bb36	tasks/archive/2026-10-27-add-flag
```

Details: [cleaning up terminal workflows](workflow-cleanup.md).

## Source cleanliness

- **Schema 2 (explicit):** only the selected aliases are checked, using each
  registered execution worktree when present. In an `existing` combined
  layout, the records subtree is excluded.
- **Schema 0/1 with `--repo`:** the config repository plus selected siblings,
  excluding this change's own records.
- **Schema 0/1 without `--repo`:** not checked (Git-blind).

Invalid aliases or bindings fail closed.

## Crash safety and locking

- `done` and `abandon` write the terminal state, then move the directory. If
  interrupted, re-run the same command: it completes the move and keeps the
  stored `verified`, evidence, and finish date.
- `new`, `phase`, `done`, and `abandon` take `<workflow-root>/tasks/.to.lock`.
  A concurrent command fails fast. A lock whose recorded pid is no longer
  running is reclaimed automatically; a lock with no readable pid must be
  removed by hand.
- Managed records mode: the archive destination and date are fixed before
  the history journal is written, so a midnight rollover can't split them.
  After a checkpoint failure run `homonto workspace recover` first.

## State file

`to-state.yaml` (schema 1), written only by the binary. `repos`,
`repo_mode`, and `repo_bases` record the selected sources and their frozen
anchors.

## What to deliberately doesn't do

No evidence gates, spec deltas, dependency graph, or automatic integration.
Escalation is an explicit conversion (`to promote`), never a phase.
