---
name: to-plan
description: to phase 1 — plan. Use when an active change has phase plan — writes docs/tasks/<name>/plan.md as a short list of bite-sized, verifiable tasks, then advances the change to do.
---

# to-plan — Phase 1: Plan

Turn the request into a short, executable plan. The plan is a reviewable git
artifact — write it for the person who reads the PR, not for yourself.
Apply the dispatcher's shared autonomous workflow policy throughout.

## Entry check

- `to status --json` shows the change at `phase: plan`.
- If `plan.md` already has content, a previous session started planning —
  read it and continue rather than starting over.

## Steps

1. **Understand before writing.** Ground every claim about the codebase in
   reading. Read the repository's relevant ADRs and nearby design documents
   before planning a behavior or architecture change. For questions that span
   many files, dispatch `to-explorer` — read-only, so run one per question
   concurrently rather than serializing them — and work from its conclusions.
2. **Choose isolation.** Follow repository policy. Reuse a suitable current
   change branch; otherwise create a branch. If unrelated work makes that unsafe,
   use a worktree or leave the unrelated paths untouched. Do not ask the user to
   choose a reversible Git mechanic.
3. **Write `docs/tasks/<name>/plan.md`:**
   - A two-or-three-sentence statement of the goal, the chosen approach, and
     the important boundary (what this change deliberately does not do).
   - An ordered task list. Every task must be executable from cold context and
     use this compact contract:

     ```markdown
     - [ ] <Concrete outcome>
       - Files: `<paths and, when useful, symbols>`
       - Change: <behavior or contract to add, remove, or preserve>
       - Verify: `<exact command>` — <specific passing signal>
     ```

   - Keep one concern in each task and keep its implementation and focused
     tests together. Name dependencies only when order is not obvious. Resolve
     unknowns before advancing; "investigate", "handle edge cases", and "add
     tests" are not executable tasks without a named question, behavior, or
     case.
   - The list is expected to grow during `do`: discovered work is appended
     with the same full contract, its outcome line suffixed
     `(discovered <date>)`, placed after the existing tasks and before
     `Final Verify:`. Plan for that by writing tasks other sessions can trust
     — a fresh session resumes from the first unchecked task.
   - When the implementation changes durable architecture or contradicts an
     existing guide, design document, or ADR, include the smallest required
     documentation task. Do not create design ceremony for an implementation
     detail that existing documentation does not promise.
   - Reserve `## Notes` for decisions, scope clarifications, and declined
     review findings discovered during execution. Do not duplicate the task
     list there.
   - A `Final Verify:` line after the tasks: the narrowest command that proves
     the whole change works, plus the expected success signal. This distinct
     label prevents it from being confused with a task's nested `Verify:`.
   - When drafting or repairing a task contract, use
     [the good/bad examples](references/task-examples.md) to test whether an
     implementer could execute it without inventing scope.
4. **De-slop it.** Run the `to-no-slop` rules over the plan prose.
5. **Check the scope boundary.** If the plan grew beyond what the user asked,
   ask about that product scope change. Otherwise proceed without plan approval.
6. **Advance:** `to phase <name>`. The change is now at `do`; hand off to the
   `to-do` skill and continue in the same invocation unless the user named plan
   as the endpoint or asked to pause.

## Rules

- Keep the plan under a screen where possible. A plan nobody reads is
  ceremony, and ceremony is what to exists to avoid.
- A task is bite-sized when one implementer can finish and verify it without
  inventing requirements. Split by independently reviewable behavior, not by
  arbitrary file count or by separate "code" and "tests" tasks.
- Never hand-edit `to-state.yaml`; the binary owns it.
- If the work turns out to need evidence-gated phases, spec deltas, or a
  dependency graph, say so: that is onto-shaped work, and this repo chose to.
  Do not rebuild onto inside a plan.md.
