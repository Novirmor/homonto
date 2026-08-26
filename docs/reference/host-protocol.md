# Host Protocol Reference

Hosts coordinate work through versioned JSON. The current
`protocol_version` is `1`; a payload with another version, unknown fields, or
trailing data is refused.

## Work Execution Loop

The normal loop is:

```text
homonto next --json   -> current action or action group
host executes action
homonto report        <- role report
```

When `next` returns a decision or edit action, the host presents the available
choices or granted document regions to a human. The host submits the result
with `decide` or `accept-edit`.

`next` returns one of these states:

| State | Meaning |
|---|---|
| `ready` | `actions` contains one or more actions that may run in parallel. |
| `blocked` | `actions` contains one human decision or edit action. |
| `complete` | `actions` is an explicit empty array. |

Requesting `next` while a group is outstanding returns the same action IDs and
freshness tokens. It does not create another group.

## Actions

Every action identifies its workflow, phase, reason, prompt, target repository,
working directory, freshness token, and input fingerprints.

| Kind | Host responsibility |
|---|---|
| `assignment` | Run the named explorer, implementer, reviewer, or skeptic in the issued directory and scope; submit the expected report shape. |
| `decision` | Show every choice and collect any required rationale or answer. |
| `edit` | Edit only the granted regions of the named workflow document, then accept the edit with its grant token. |

An assignment with `write_scope.read_only` writes nothing. A writable scope lists
paths relative to its working directory. An empty writable scope is never
issued.

## Reports And Freshness

`report` accepts a `ReportSubmission` from stdin or `--file`. It includes an
action ID, freshness token, role, session details, and role-specific report.
Implementer reports name either a Git commit or a snapshot patch manifest.

Homonto checks the resulting on-disk changes before it records a writable
assignment report. A stale, duplicate, unknown, wrong-role, or out-of-scope
report is refused. Request a new action after invalidation instead of reusing
old credentials.

## Guard And Host Probes

`guard` evaluates a protocol `GuardRequest` from stdin or `--file`. It accepts
assignment credentials through `--action` and `--token`, and edit-grant
credentials through `--grant` and `--grant-token`. A refusal writes its JSON
decision to stdout and exits with code `2`.

`host guard` translates host-specific hook input into the same decision.
`host probe` is a read-only resume query. It returns `idle`, `resumable`,
`ambiguous`, or `unavailable`; it does not initialize a directory, migrate a
workspace, or choose between active works.

See the [CLI reference](cli.md) for command syntax and the [security boundary](security-boundary.md)
for the write gate's limits.
