# One anchored work per workspace, resumed at the phase start

- **Status:** Accepted
- **Date:** 2026-08-25

## Context

WS2 built the portable record — the committed checkpoint, the member
leases, the sentinel — and nothing called it. No command took a lease,
wrote a sentinel, or produced a first checkpoint, so `homonto handoff`
failed with "no such file or directory" on every real workspace and
`homonto attach` had nothing to consume. Wiring it raised two questions
the machinery does not answer on its own.

**Several works, one set of members.** The Task engine supports more than
one active work in a workspace: `next` refuses to guess between them
rather than forbidding them. But a lease is exclusive per member per work,
so the second work cannot lease what the first holds.

**What travels.** The checkpoint is content-free by design: it carries the
phase a work reached and nothing about what happened inside it. The
workflow's own state machine — steps, generations, assignments, evidence —
lives in the runtime database, which is explicitly not portable.

## Decision

**Only the workspace's first active work is anchored.** Starting a work
leases every member and writes the checkpoint only when no other work
already holds them. A second concurrent work runs normally, sharing the
same members, but is not anchored; `homonto handoff` then refuses it
because it is not the workspace's single active work. Archiving or
abandoning releases the hold, and the next work anchors.

**An attached work resumes at the FIRST step of the recorded phase.** A
Task maps phase to step directly. A Change first recovers its path from
the input document on disk — each path writes a different one, so which is
present says which path was confirmed — and then does the same. Resuming
is idempotent: a work that already has local state is left alone.

## Consequences

Handing over one of two concurrent works is impossible, and says so. That
is the honest position: the members the second work needs are held by the
first, and a checkpoint promising otherwise would fail on the receiving
machine rather than here.

Resuming re-enters a phase rather than continuing mid-phase. Work already
done inside that phase is re-derived from the documents and the members —
the same thing reconciliation does when a recorded step turns out to rest
on something that moved. Claiming the exact step would claim evidence that
never travelled.

`checkpoint.Validate` now accepts the control repository as a checkpoint
member. Attach already read a control entry; refusing one here made a
checkpoint that carried the control unvalidatable, which is not the same
as saying it may not carry one.
