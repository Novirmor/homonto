# Recovery

Homonto assumes it will be interrupted. Every durable operation is
journaled, every effect is idempotent, and the next invocation finishes or
undoes whatever the last one started.

## Crashes

State changes run as journaled operations with per-effect records. On the
next open, pending operations are driven to a terminal state before
anything new starts — building on an unfinished operation is how a
half-applied effect becomes permanent.

You do not run anything to make this happen. Opening the workspace does it.

## Invalidation is not recovery

Distinct problem, distinct mechanism. A crash is "an operation did not
finish"; invalidation is "the world moved under a step that had finished".
Reconciliation handles the second, on every `next`, by comparing the
baseline the recorded step rests on against what is there now. See the
workflow guides for the per-cause tables.

## Handoff

Work is anchored to the machine holding its leases. To move it:

```bash
homonto handoff
```

That marks the active work's checkpoint transferable, commits it to the
control repository, and releases this machine's leases. Nothing local is
destroyed — the work simply stops being anchored here.

Push the control repository. Elsewhere:

```bash
git clone <control-repository> && cd <clone>
homonto attach --propose
```

`--propose` shows where Homonto thinks each member lives here, and how
confident it is:

| Status | Meaning |
|---|---|
| `exact` | One candidate at the member's configured path, with the right kind |
| `changed` | No path match, but one candidate shares a declared remote |
| `ambiguous` | Several match equally — you choose |
| `missing` | Nothing matches |

Confirm each one:

```bash
homonto attach --member <repository-id>=/path/to/member
```

Mappings are **confirmed, not guessed**. A wrong mapping does not fail
loudly: it attaches the work to the wrong repository, and every assignment
after that is issued against the wrong tree.

Attach claims each member's registration, takes the full lease set,
rebuilds the local runtime from the portable record, marks the checkpoint
consumed, and commits. It is all-or-none.

## What travels

The checkpoint carries portable, stable facts. It does not carry the
runtime database, raw report text, command output, or local recovery
tokens. Attach **rebuilds** the runtime from the checkpoint, the documents,
and the configuration — and records source fingerprints as **unverified**,
because a fingerprint carried across machines is evidence of the other
machine's observation, never of this one's.

Attach also mints a fresh runtime key, so every action id and freshness
token from before the handoff fails closed. That is not a side effect; it
is the point.

## Forced takeover

If the checkpoint was already consumed elsewhere — a machine that crashed,
a laptop in a drawer — you can take it anyway:

```bash
homonto attach --force --member …
```

That increments the generation, records a `forced_takeover` decision, and
marks **every recorded fact stale**. Nothing advances until it has been
re-verified. The old machine's checkpoint generation no longer matches, so
it refuses further mutation the next time it wakes up.

There is no timeout-based automatic takeover. A machine that has not been
seen for a while has not necessarily stopped working.

## Non-Git members

Non-Git isolation is content-hashed snapshots: blobs, a base manifest, and
a materialized work tree. Blobs are shared and content-addressed, and are
never garbage collected — an interrupted capture leaves reusable content
rather than a partial tree.

A recovery that finds the source changed since the first capture fails
closed. The recorded base is the truth; a re-capture of different content
never overwrites it.

## Interrupted self-update

See [updates](updates.md). The short version: the next invocation finishes
or rolls back the journaled generation before anything else runs, and an
unreadable update journal blocks ordinary commands rather than letting them
run against an installation nobody can describe.

## When something will not open

```bash
homonto doctor
```

reports membership drift, host-integration drift, ambiguous active work,
and an interrupted update — with a remedy for each. It never repairs.
