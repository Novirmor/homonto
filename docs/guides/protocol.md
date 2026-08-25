# Protocol

Everything a host tool does with Homonto goes through this. It is versioned
JSON, strictly decoded — unknown fields and trailing data are refused — and
deterministic: the same value always encodes to identical bytes.

`protocol_version` is currently `1` and must match exactly.

## The loop

```
homonto next --json        →  what to do now
homonto report             ←  a role report, on stdin
homonto decide             ←  a human's answer
homonto accept-edit        ←  the host finished a document edit
```

That is the whole surface. A host that finds itself reasoning about
workflow rules is a host doing something the binary should be doing.

## `homonto next --json`

```json
{
  "protocol_version": 1,
  "state": "ready",
  "actions": [ … ]
}
```

`state` is one of:

| State | Meaning | Actions |
|---|---|---|
| `ready` | Run these now, in parallel | one or more |
| `blocked` | One human decision | exactly one |
| `complete` | Nothing left | an explicitly empty array |

The empty array is explicit, never an omitted key.

**Re-asking while a group is outstanding returns the same group**, with the
same ids and the same freshness tokens. It is safe to repeat, and it is not
new work.

## Actions

Every action carries an `id`, a `freshness_token`, the `workflow`, the
`phase`, a `reason`, a `prompt`, the `repository` it targets, a
`working_directory`, a `write_scope`, and the `input_fingerprints` it rests
on. Three kinds differ beyond that.

### `assignment`

Subagent work. Carries a `role` — `explorer`, `implementer`, `reviewer`, or
`skeptic` — and an `expected_report` naming the schema to answer with.

```json
{
  "kind": "assignment",
  "role": "implementer",
  "working_directory": ".homonto/worktrees/…/…",
  "write_scope": { "read_only": false, "paths": ["src"] },
  "expected_report": { "kind": "implementer", "schema_version": 1 }
}
```

`write_scope.read_only` means the assignment writes nothing at all.
Otherwise `paths` is what it may write, relative to `working_directory`.
The scope is always explicit; an empty writable scope is never issued.

### `decision`

A human gate. Carries a `decision` schema: the `kind`, the `prompt`, and
the `choices`, each with a `value`, a `label`, and whether it
`requires_rationale`.

Show all of them. Do not pick one, and do not recommend one unless asked.
An empty choice is refused: silence is not approval.

### `edit`

The **host's own** write, not a subagent's. Carries an `edit` permission:

```json
{
  "kind": "edit",
  "edit": {
    "grant_id": "…",
    "grant_token": "…",
    "document": "docs/homonto/tasks/fix-login.md",
    "document_kind": "task",
    "regions": ["task-goal", "task-checklist"]
  }
}
```

Edit only those regions of that document. Anything else — a change to
another region, to the metadata block, or to the region markers — is
refused and the action stays open. Then:

```bash
homonto accept-edit --action <id> --token <grant_token>
```

You present the token; Homonto looks up what the grant actually opened
rather than believing a structure you hand back.

## `homonto report`

Reads a `ReportSubmission` on stdin:

```json
{
  "protocol_version": 1,
  "action_id": "…",
  "freshness_token": "…",
  "role": "implementer",
  "session": { "host_id": "…", "hostname": "…", "pid": 8412, "executable": "…", "started_at": "…" },
  "report": { … }
}
```

`report` is the schema `expected_report` named:

| Role | Fields |
|---|---|
| `explorer` | `facts`, `constraints`, `surfaces`, `tests`, `questions` |
| `implementer` | `material`, `changed_paths`, `assignment_checks`, `questions` |
| `reviewer` | `acceptance`, `findings`, `questions` |
| `skeptic` | `assumptions`, `findings`, `questions` |

`material` is how the work travels: `git_commit` with a `commit`, or
`snapshot_patch` with a `patch_manifest`. A Git member's implementer
commits; a non-Git member's returns a manifest.

`findings` carry a `severity` — `critical`, `high`, `medium`, `low`.
Critical and high block until fixed, withdrawn, or explicitly accepted.

**A report is validated against what actually changed on disk before it is
recorded.** A report backed by out-of-scope changes is refused, and the
action stays answerable. Fix the change; do not retry the report.

## `homonto decide`

```bash
homonto decide --action <id> --token <freshness_token> \
  --choice <value> [--rationale <why>] [--answer <text>]
```

Or the same payload as JSON on stdin. A rationale is required exactly when
the chosen option says so. `--answer` is for question gates and is refused
elsewhere.

## Freshness

Every action carries a token that is HMAC-SHA256 over the runtime key and
the action id. Nothing is stored, so it is re-derived on demand — and every
token minted before an `attach` fails closed, because attach mints a new
key.

Staleness is carried by **state**, not by the token. An invalidated action
keeps a derivable token and still refuses every submission, and re-issuing
work mints new action ids, so an old id can never be answered.

## `homonto guard`

The write gate, for a cooperating host's hook. See
[security](security.md); hosts normally reach it through
`homonto host guard`, which speaks the host's own event shapes.

## `homonto host probe`

The read-only resume probe. It performs no write, no migration, and no
network access — a host runs it at the start of every session, and starting
a session must change nothing.

```json
{
  "protocol_version": 1,
  "state": "resumable",
  "work": { "id": "…", "name": "fix-login", "kind": "task", "step": "do_implement", "workflow": "task" },
  "message": "…"
}
```

`state` is `idle`, `resumable`, `ambiguous`, or `unavailable`. A directory
that is not a Homonto workspace answers `unavailable` rather than failing:
a host runs this everywhere.

With more than one active work the probe reports `ambiguous` and lists the
candidates. It does not choose. Neither should you.
