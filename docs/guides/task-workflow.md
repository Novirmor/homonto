# Task workflow

`plan → do → done`. For work that needs doing carefully but does not need a
paper trail of decisions. One file in, one archived record out.

```bash
homonto task start fix-login --goal "Login fails after a restart."
homonto next --json      # the host does this; you can watch
homonto task status
```

## The steps

The steps are an enumerated list and the transitions are a switch. There is
no workflow definition language and no rule table loaded from
configuration: a workflow you can express in data is a workflow whose
invalid states you cannot see.

| Step | What happens | What ends it |
|---|---|---|
| `plan_explore` | One explorer per confirmed member surveys the workspace | every explorer reports |
| `plan_draft` | **You** write the goal and checklist, under an edit grant | the edit is accepted |
| `plan_challenge` | A skeptic attacks the assumptions, missing cases, and scope | the skeptic reports |
| `plan_resolve` | Every consequential question it raised is put to you | every question is answered |
| `do_implement` | Parallel implementers, one isolation area each | every implementer reports |
| `do_integrate` | One integration implementer per member | integration reports |
| `done_checks` | **Homonto** runs your configured checks | passed → review, failed → repair |
| `done_review` | A reviewer and a skeptic assess the integrated result | clean → finalize, blocked → repair |
| `do_repair` | A bounded repair round | done → integrate |
| `done_finalize` | Evidence appended, record archived | — |

Terminal: `archived`, or `abandoned`.

There is **no plan-approval gate**. Consequential unanswered questions block
implementation, but nobody signs off on a plan.

## What you actually do

Two of these steps are yours.

**The draft.** `homonto next` returns an `edit` action carrying a grant on
the goal and checklist regions of the task document. Edit only those
regions; anything else is refused and the action stays open. Then:

```bash
homonto accept-edit --action <id> --token <edit.grant_token>
```

**The decisions.** A `blocked` response carries exactly one decision. It
shows the prompt, every choice, and which choices need a rationale.

```bash
homonto decide --action <id> --token <freshness_token> \
  --choice answered --answer "Replace the store; the migration is one query."
```

## Parallelism

Read-only assignments run in parallel whenever their dependencies are met,
and implementers run at maximum parallelism in separate isolation areas —
including ones that may later conflict, because resolving that is exactly
what the integration assignment is for.

`homonto next` releases the **maximal** ready group: every action whose
dependencies are answered goes out together. A decision or an edit is the
exception and goes out alone — both are your turn, and neither should be
raced against agent work happening underneath it.

Asking again while a group is outstanding returns the **same** group, with
the same ids and tokens. It is safe to repeat and it is not new work.

## Checkoffs

Only Homonto checks an item, and only for an assignment it accepted — after
the final diff gate has passed. An item still unchecked at the end is an
item nothing accepted work for, and the record says so rather than tidying
it away.

## Repair

A failed check or an open blocking finding sends the task to repair. A
repair round produces new material in fresh isolation areas, so it returns
to **integration**, not straight to the checks: material that has not been
integrated is material the checks would never see.

A repair **replaces** the attempt it repairs rather than stacking on it.
The integration area restarts from the base and takes only the newest
material for each unit, so the integration branch holds the repair and not
the failed attempt — which is what the archived record claims. An
integration area holding uncommitted changes is refused instead of reset:
those are someone's unfinished conflict resolution.

Entering repair again means the previous round failed. After **three**
failed rounds Homonto stops and asks you:

- **continue** — keep repairing (the counter resets)
- **accept** — record the outstanding blocking findings as documented
  deviations. This does not make a failing check pass; the checks run again
  and if they still fail Homonto says so again.
- **abandon** — stop

All three require a rationale. After three failures there is no answer that
does not deserve one.

## Invalidation

The recorded step is never trusted alone. Every step rests on a baseline,
and `homonto next` reconciles before acting on it.

| What moved | Where it returns to |
|---|---|
| The goal or the checklist's items | `plan_draft` |
| The confirmed repository list | `plan_explore` |
| The test/generated/vendored classes | `plan_explore` |
| The verification configuration | `done_checks` |
| An integrated source fingerprint | `done_checks` |

A source change deliberately does **not** invalidate the plan: the goal did
not change just because the code did.

Two things are excluded from the plan's fingerprint, both because Homonto's
own writes must not invalidate the plan that produced them: the evidence
section, and the checkbox STATE. A checked box is progress against the
plan, not a change to it.

When a return happens it is real: the open actions are invalidated so no
in-flight host can answer them, the generation is bumped so a late answer
is refused, and evidence the return reaches back past is deleted rather
than left readable.

## Finishing

`done_finalize` appends the evidence — a short outcome, the exact commands
and outcomes, the integration fingerprints, and the accepted deviations —
and moves the file to `docs/homonto/tasks/archive/<date>-<name>.md`.

Two works archived on the same day under the same name get a `-2` suffix.
Lookups never rely on that: identity comes from the metadata block inside
the document, so a suffixed archive still resolves.

The integration branches and staged directories are left **ready**.
Homonto never merges them into a member's own branch.

## Stopping

```bash
homonto task abandon
```

Its isolation areas, branches, and evidence are left exactly where they
are. Abandoning is a decision to stop working, not an instruction to
destroy the work.
