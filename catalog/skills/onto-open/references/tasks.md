# tasks.md — canonical template

The change's checklist, and the **single source of completion state**. Open
creates the skeleton (boundaries); build refines and checks items off — one
commit per checked item. The phase derivation and the build exit gate read
these checkboxes and nothing else.

On a full change, each item's executable detail (files, what to do, how it is
verified) lives in `plan.md` under a matching `## Task N.M` heading. Same
number, one detail block per item: `- [ ] 2.3` here pairs with `## Task 2.3`
there. Items and tasks are added and superseded together, in the same edit;
only this file carries the checkbox.

## Template

```markdown
# Tasks: <change-name>

## 1. <area, e.g. Foundation>

- [ ] 1.1 <task — outcome-stated, small enough for one reviewable commit>
- [ ] 1.2 <task>

## 2. <area, e.g. Implementation>

- [ ] 2.1 <task>

## N. Validation

- [ ] N.1 <how this change proves itself — dry-runs, tests, evidence>
```

## Rules

- Checkbox syntax exactly `- [ ]` / `- [x]` (the phase-derivation table
  greps it). A deliberately deferred task uses `- [x] N.N DEFERRED to
  close: <reason>` — checked, with the deferral stated. Close is the only
  deferral target (build's exit and verify's entry recognize nothing
  else). **Only non-runtime work may be deferred** (bookkeeping, file
  moves, doc stamps — anything whose behavior verify would need to
  demonstrate must be built before verify). When close executes a
  deferred task it rewrites the line to
  `- [x] N.N (deferred, done at close YYYY-MM-DD): <desc>` — that rewrite
  is what the pre-archive lint's "no unresolved markers" check reads.
- Number tasks `<area>.<n>`; keep one outcome per task.
- **The list is live**: work discovered during build is appended as
  `- [ ] N.M (discovered <date>): <task>` — appended BEFORE its code is
  written, checked off when its commit lands. Never renumber, reorder, or
  delete existing tasks; a task made unnecessary is checked as
  `- [x] N.N SUPERSEDED: <reason>`. A fresh session resumes from the first
  unchecked task, so the checkboxes must describe reality at every commit.
- Every change ends with a Validation area — a change that can't state its
  own proof isn't ready to build.
