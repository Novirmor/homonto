# One active work per workspace, resumed at the phase start

- **Status:** Accepted
- **Date:** 2026-08-25

## Context

WS2 built the portable record — the committed checkpoint, the member
leases, the sentinel — and nothing called it. No command took a lease,
wrote a sentinel, or produced a first checkpoint, so `homonto handoff`
failed with "no such file or directory" on every real workspace and
`homonto attach` had nothing to consume. Wiring it raised two questions
the machinery does not answer on its own.

**Several works, one set of members.** The engines let more than one work
exist at once — `next` refuses to guess between them rather than
forbidding them — while the specification says exactly one top-level Task
or Change may be active in a workspace, with parallelism happening inside
that work. A lease is exclusive per member per work, so the second work
could not lease what the first holds anyway.

**What travels.** The checkpoint is content-free by design: it carries the
phase a work reached and nothing about what happened inside it. The
workflow's own state machine — steps, generations, assignments, evidence —
lives in the runtime database, which is explicitly not portable.

## Decision

**Starting a second work is refused.** `homonto task start` and
`homonto change start` check for an active work first and name the one in
the way. Starting the one work leases every member, writes the sentinel,
and commits the first checkpoint; archiving or abandoning releases the
hold, and the next work anchors.

**An attached work resumes at the FIRST step of the recorded phase.** A
Task maps phase to step directly. A Change first recovers its path from
the input document on disk — each path writes a different one, so which is
present says which path was confirmed — and then does the same. Resuming
is idempotent: a work that already has local state is left alone.

## Consequences

Two top-level works can no longer be started, so the ambiguity `next` and
the resume probe describe is reachable only from state that predates this
rule or was repaired by hand. Both keep handling it — refusing to choose
is still the right answer — and the tests construct that state through the
engines rather than through the commands.

Resuming re-enters a phase rather than continuing mid-phase. Work already
done inside that phase is re-derived from the documents and the members —
the same thing reconciliation does when a recorded step turns out to rest
on something that moved. Claiming the exact step would claim evidence that
never travelled.

`checkpoint.Validate` now accepts the control repository as a checkpoint
member. Attach already read a control entry; refusing one here made a
checkpoint that carried the control unvalidatable, which is not the same
as saying it may not carry one.
