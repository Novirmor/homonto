# Hand worker permission blocks to the coordinator

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

Workers run with narrower permissions than the coordinator. When a worker's
tool call was denied, or the user rejected one of its prompts, the only rule
was "honor the denial". The assignment dead-ended: the coordinator often
couldn't tell which command was refused, couldn't tell a role boundary from a
user refusal, and the user had no path to unblock the work short of starting
again. A rejected prompt can also end a worker's session before it returns
anything.

## Decision

A permission block hands the assignment back to the coordinator; it does not
end the task. Every worker stops at a denied or rejected call and returns a
`Permission block`: the tool, exact command or resource, the denial text, why
the work needs it, and what is already done. It never retries or switches
tools.

The coordinator triages before asking: is the action needed or does an allowed
equivalent exist; is it a static role boundary the coordinator may cross
within its own authority, or a user rejection that only the user can reverse.
What remains is asked once through the native dialog: the coordinator runs it
with approval, the user supplies the output or the information, the gap is
recorded, or the scope changes. A free-form reply, in the dialog or in chat if
the dialog is unavailable, resolves the block, and a fresh worker resumes with
the resolution.

Both workflow plugin variants record rejected prompts from child sessions
(tracked through `session.created`'s `parentID`) and add one bounded notice to
the parent's next model context, so the block is visible even when the worker
returned nothing. The notice is data. It grants nothing and is consumed once.

We rejected auto-approving worker prompts, and a plugin that re-runs refused
commands in the coordinator: both would replace the user's decision.

## Consequences

Blocked work resumes without restarting the task, at the cost of one more
coordinator decision per block. Running a role-boundary action directly is
ownership, not evasion, but the coordinator still holds its own tool prompts
and publication rules. Static denies may not emit permission events, so those
blocks rely on the worker's return. Event ordering can deliver the notice one
model request late. Shipped-prompt tests cannot prove that models follow the
triage.
