# plan.md — canonical template

The executable detail behind the confirmed design: for each task, which files,
what to do, and what proves it. A task that can't state its verification isn't
ready.

**`plan.md` holds no completion state.** `tasks.md` is the single checkoff —
the phase derivation and the build exit gate read its checkboxes and nothing
else. Task numbers here match `tasks.md` item numbers, so the two are read
together without being maintained twice.

## Template

```markdown
# Plan: <change-name>

Design: `design.md` (Status: Confirmed <date>). Completion state lives in
`tasks.md`. One commit per task.

## Task N.M — <outcome, imperative>  <!-- add `(risk: high)` when it warrants a reviewer -->

- Files: <exact paths created/modified>
- Do: <what, concretely — reference design sections, don't restate them>
- Verify: <the command(s)/check(s) that prove this task done>
```

## Rules

- Bite-sized: one reviewable commit (~200 lines of change) per task —
  split anything bigger.
- `(risk: high)` marks tasks that get a reviewer agent under
  `execution: subagent` (and deserve extra scrutiny under `direct`).
- **Number tasks to match `tasks.md`.** `## Task 2.3` here is the detail for
  `- [ ] 2.3` there. Check the task off in `tasks.md` only; never add a
  checkbox here.
- **The plan is live**: work discovered during execution is appended as
  `## Task N.M — <outcome> (discovered <date>)` with the same Files/Do/Verify
  fields, BEFORE its code is written — and appended to `tasks.md` in the same
  edit, which is where its checkbox lives. Numbering only grows; never
  renumber or delete a task. A task that becomes unnecessary keeps its heading
  and gains a `SUPERSEDED: <reason>` line, matching the
  `- [x] N.M SUPERSEDED: <reason>` entry in `tasks.md`.
- The final task is always validation (the change proving itself). Appended
  tasks land BEFORE it — validation stays last.
