---
name: to-do
description: to phase 2 — do. Use when an active change has phase do — executes plan.md one task at a time through the implementer/reviewer loop, one implementer at a time with reviewers run concurrently, holding the code to onto-grade standards.
---

# to-do — Phase 2: Do

Execute the plan, one task at a time. This is the code-writing skill: the flow
is simple, but the code written inside it is held to the full bar.

## Entry check

- `to status --json` shows the change at `phase: do`.
- `plan.md` has a task list whose entries state a concrete outcome and
  non-empty `Files:`, `Change:`, and `Verify:` fields, plus one non-empty
  `Final Verify:` line. If it does not, repair the plan before implementation;
  do not make the implementer invent the missing contract.
- On resume (fresh session, context loss): run `to handoff <name>` first, then
  find the first unchecked task in `plan.md` and continue from there; never
  redo completed tasks.

## The loop

For each unchecked task in `plan.md`, in order:

1. **Dispatch `to-implementer`** with the complete task verbatim: outcome,
   files and symbols, behavioral contract, verification command, and expected
   passing signal. Include any directly relevant conclusion from the plan's
   grounding; do not silently add scope. One implementer at a time — **never in
   parallel**: it is the only agent that edits, and `to` keeps a single working
   tree.
2. **Verify against the repository**, not the report: check the diff exists
   and the task's verification command passes.
3. **Dispatch `to-reviewer`** with the original task contract, the resulting
   diff, and the verification result. Ask it to judge both correctness and
   whether the stated outcome is actually complete. Reviewers are read-only and
   may run concurrently — on a task worth more than one angle, dispatch several
   at once with distinct lenses (correctness, security, completeness) rather
   than one after another. Always **after** the implementer returns, never
   alongside it: reviewing a tree still being written reviews nothing.
4. **Act on findings.** Fix critical/major findings before moving on
   (re-dispatch the implementer for substantial fixes; apply trivial ones
   directly). Declined findings are recorded with a reason under `## Notes` in
   `plan.md` — never silently dropped.
5. **Check off the task** only when its stated outcome is present and its exact
   verification has the expected result. Commit one task at a time with a
   message that names the outcome. De-slop the message.

Small tasks (a rename, a doc line) may skip the subagent loop and be done
directly — but never skip the verification command or the commit.

**The plan is live state.** Discovered work — a missing edge case, a
prerequisite, a forgotten test — is APPENDED to `plan.md` as a new unchecked
task (full contract: Files/Change/Verify, outcome line suffixed
`(discovered <date>)`, placed before `Final Verify:`) **before** its code is
written; append-then-do, never do-then-maybe-note. Check off only at the
task's own commit; never reorder or delete tasks — a task made unnecessary is
checked as `- [x] SUPERSEDED: <reason>`. A fresh session resumes from the
first unchecked task, so if the checkboxes ever stop describing reality, fix
the plan before writing more code. Work that breaks the plan's stated
boundary is not "discovered work" — confirm the scope change with the user
first.

## Code-writing standards (every task, no exceptions)

- Read the surrounding code before changing it; match its style, naming,
  idioms, and comment density.
- The smallest change that satisfies the task; no unrelated refactors.
- Behavior changes get focused tests, added or updated in the same task.
- Run the narrowest useful verification and read its output; a green run you
  didn't read is not verification.
- No symptom patches: when something fails unexpectedly, find the root cause
  before changing anything.

## Exit

All tasks checked and the plan's `Final Verify:` command passing → load the
`to-done` skill. Do not run `to done` from here; finishing is `to-done`'s job.
If the work is not worth finishing, `to abandon <name>` and tell the user why.
