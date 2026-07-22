---
name: onto-open
description: onto phase 1 — open a change. Use when starting a new change or when the dispatcher routes here (phase open) — clarifies requirements, checks for scope splits, and creates the change workspace with a proposal (the task list is derived later, in design).
---

# onto-open — Phase 1: Open

Turn an idea, feature request, or problem statement into a change workspace
with an unambiguous proposal. Nothing is designed and nothing is built here.

## Entry check

- No workspace exists yet for this work, **or** the workspace's `onto-state.yaml`
  says `phase: open` with `workflow: full`.
- Bug fixes and small tweaks belong to `onto-fix` / `onto-tweak` — if the
  request fits a preset, hand over to it instead.
- If the workspace has a `notes.md`, read it first — resume from its
  Pending items; never re-ask what Confirmed already answers.
- Any other state → route back through `/onto` (the dispatcher rederives the
  real phase).

## Steps

### 1. Clarify

Ask questions until the requirement is unambiguous — one topic at a time,
multiple-choice where possible. Do not treat a single Q&A round as enough
for anything non-trivial. Ground every claim about the existing codebase in
graphify/codegraph queries when available — the preflight may have
recorded a direct-file-reading fallback in notes.md Grounding; grounding
in real file reads is required either way, guesswork never is.

The clarification must end in a summary covering:

- **Goals** — the problem actually being solved, expected outcome
- **Non-goals** — explicitly out of scope
- **Scope boundaries** — modules/users/platforms/data in and out
- **Key unknowns** — open assumptions, risks, dependencies
- **Draft acceptance scenarios** — core success path + important edge cases

> **GATE (clarification complete):** present the summary and ask the user to
> confirm it before creating any artifact. Always requires fresh input —
> a blanket "run to completion" directive does NOT pre-answer this gate.

### 2. Split preflight

If the request spans multiple independent capabilities, journeys, or
milestones — anything that could be designed, built, verified, and closed
independently — propose a split: per item, a name, goals, non-goals,
dependencies, and core scenarios.

> **GATE (split decision, only when a split is proposed):** the user chooses
> "split into separate changes", "keep as one (record why)", or "adjust the
> split". Each accepted item becomes its own change via this skill. Always
> fresh input.

### 3. Create the workspace

Create `docs/changes/<name>/` (name confirmed by the user, kebab-case),
each artifact from its canonical template:

- Create the workspace via the binary: `onto new <name> --workflow full`
  (`onto new` creates `onto-state.yaml` carrying `change`, `workflow: full`,
  `phase: open`, `created`; and an empty `proposal.md`. It does **not** scaffold
  `tasks.md` — a full change's task list is derived from the confirmed design in
  onto-design, not written here). Then record the creation fields the same way:
  - `onto set base-ref <name> "$(git rev-parse HEAD)"` — captured NOW, before
    anything is committed; written once, never recomputed.
  - `onto set deps <name> --dep <a> --dep <b>` for each `Depends-on:` entry
    (omit entirely when there are none).
- `notes.md` — template: `references/notes.md`. Created NOW, seeded with
  the confirmed clarification summary. From this point, update it before
  ending **any** turn that produced new decisions — this is the
  compaction-recovery checkpoint.
- `proposal.md` — template: `references/proposal.md`; fill the skeleton `onto
  new` created. State the intended scope, non-goals, and acceptance scenarios;
  the concrete task list is **not** written here — it follows from the design.

Everything in the proposal must trace back to the confirmed clarification
summary — no invented scope.

> **GATE (artifact review):** summarize the proposal and ask the user to approve
> or request adjustments. Iterate until approved. Always fresh input. When
> approved, record the evidence token — the binary refuses to leave open
> without it:
>
> ```
> onto set proposal-approved <name> "YYYY-MM-DD <one-line approval summary>"
> ```

## Exit checklist

- [ ] Workspace exists with `onto-state.yaml`, `notes.md`, `proposal.md`,
      all template-conformant and consistent with the confirmed summary
      (no `tasks.md` yet — it is derived in design)
- [ ] `notes.md` Confirmed section reflects every answered gate
- [ ] `proposal.md` `## Grounding` is filled — the queries run, or the
      recorded fallback if graphify was unavailable/declined; never left
      blank (the close lint blocks a blank Grounding at archive)
- [ ] Every gate that fired answered by the user (clarification, split
      when one was proposed, artifact review)
- [ ] `onto set proposal-approved <name> "<evidence>"` recorded — `onto
      advance` refuses to leave open without it
- [ ] Phase advanced open → design via `onto advance <name>` — run **only
      after** the artifact-review gate is answered, never before (the
      dispatcher treats a lagging phase as an unanswered gate and re-presents it)
- [ ] onto-no-slop pass run over `proposal.md` and `notes.md`, the pass
      recorded in `notes.md` (`no-slop: <artifact> done`)
- [ ] **Commit the workspace**: `git add docs/changes/<name> && git commit`
      — every phase exits with its workspace committed; state recovery,
      `base_ref` rebuild, and the close-phase `git mv` all depend on the
      workspace being tracked
- [ ] Announce the transition and load `onto-design`
