---
name: to-plan
description: to phase 1 — plan. Use when an active change has phase plan — writes <workflow-root>/tasks/<name>/plan.md as a short list of bite-sized, verifiable tasks, then advances the change to do.
---

# to-plan — Phase 1: Plan

Turn the request into a short, executable plan. The plan is a reviewable git
artifact — write it for the person who reads the PR, not for yourself.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry.

## Entry check

- `to status --json` shows the change at `phase: plan`, or the dispatcher routes
  a converted do change here for incomplete-contract repair. That repair preserves
  recorded do; skip `to phase` on exit and return to `to-do` after contracts pass.
- If `plan.md` already has content, a previous session started planning —
  read it and continue rather than starting over.
- In schema 2, inspect `repo_bases` already frozen by the dispatcher's `to new`.
  Alternative bases must be selected there with repeated
  `--base <alias>=<local-branch>`, such as `--base api=main --base web=develop`,
  not by switching a dirty original or retargeting state during planning. The
  default freezes each source's current committed HEAD and local branch.
  Report an existing base mismatch; never recreate the change to change anchors.

## Steps

1. **Understand before writing.** Ground every claim about the codebase in
   reading. Read the repository's relevant ADRs and nearby design documents
   before planning a behavior or architecture change. For questions that span
   many files, dispatch `to-explorer` — read-only, so run one per question
   concurrently rather than serializing them — and work from its conclusions.
   For converted work, carry Owner/Repo/Cwd and Files/Change/Verify from the
   preserved snapshot when concrete, then validate against the actual records
   owner/source binding. Do not drop ownership/root fields during translation.
   Missing or ambiguous fields require plan repair before execution, not defaults
   inferred from a converted phase or a doctor's partial contract check.
2. **Validate isolation and fit.** Check bounded fit before allocation; if onto
   obligations emerge after a binding exists, follow the dispatcher's explicit
   conversion-blocker handoff, not an unsupported promotion attempt.
   Schema 2 allocation belongs immediately after `new`, before any records/source
   commit. Reuse that binding here. Inspect before writes and present exact dirty paths.
   Honor an existing preserve/isolate/cleanup decision; ask once if none exists.
   Reuse a safe source branch or create one. When isolation is chosen, allocate
   `homonto worktree create <name> --workflow to --repo <alias> --base <ref> --branch <branch> --json`
   from configRoot, after active state selects that alias. Use the recorded local
   target as `--base` (for example `refs/heads/main` after `to new` selected
   `--base api=main`); both its exact frozen commit and target must match.
   A dirty feature checkout remains untouched. Validate with
   `homonto worktree list --json`; schema 2 has no raw/native unregistered execution paths.
   Legacy schema 0/1 serial combined isolation needs no `worktrees.dir`; use the
   shared policy's single coordinator change checkout, never registered allocation.
   Keep workflow calls on `--dir "<configRoot>"` even from sources. Never copy
   `.env` or required dirty input without the user's transport decision.
3. **Write `<workflow-root>/tasks/<name>/plan.md`:**
   - A two-or-three-sentence statement of the goal, the chosen approach, and
     the important boundary (what this change deliberately does not do).
   - An ordered task list. Every task must be executable from cold context and
     use this compact contract:

     ```markdown
      - [ ] <Concrete outcome>
        - Owner: <implementer for source; coordinator for workflow records>
        - Repo: <selected alias, legacy config source, or records Git owner>
        - Cwd: `<absolute validated execution root or records root>`
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
   - Substantial records tasks (ADRs, guides, specs, plans) remain coordinator-owned
     and serial, not implementer work. Split mixed-root source/records work into
     linked tasks, each with its own Owner/Repo/Cwd and verification.
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
6. **Record and advance:** in managed mode checkpoint manual Markdown first:
   `homonto workspace checkpoint --path tasks/<name>/plan.md --message "Record plan"`.
   Existing mode retains its named manual records commits. Run
   `to phase <name> --dir "<configRoot>"` only at recorded plan; its managed checkpoint is automatic.
   For converted do contract repair, skip the phase call and retain do.
   The change is now at `do`; hand off to the
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
  dependency graph, say so: that is onto-shaped work. `to promote <name> --yes`
  converts the change; do not rebuild onto inside a plan.md.
