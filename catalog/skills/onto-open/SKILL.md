---
name: onto-open
description: onto phase 1 — open a change. Use when starting a new change or when the dispatcher routes here (phase open) — clarifies requirements, checks for scope splits, and creates the change workspace with a proposal (the task list is derived later, in design).
---

# onto-open — Phase 1: Open

Turn an idea, feature request, or problem statement into a change workspace
with an unambiguous proposal. Nothing is designed and nothing is built here.
Apply the dispatcher's shared autonomous workflow policy throughout.

## Entry check

- No workspace exists yet for this work, **or** `onto state <name> --json`
  reports `phase: open` or a dispatcher-routed `derived_phase: open`, with
  `workflow: full`.
- Bug fixes and small tweaks belong to `onto-fix` / `onto-tweak` — if the
  request fits a preset, hand over to it instead.
- If the workspace has a `notes.md`, read it first — resume from its
  Pending items; never re-ask what Confirmed already answers.
- Any other state → route back through `/onto` (the dispatcher rederives the
  real phase). On a downward mismatch, repair open artifacts without changing
  the later recorded phase.

## Steps

### 1. Clarify

Identify material gaps in goals, behavior, constraints, and acceptance criteria.
Resolve codebase facts by investigating; ask one focused question at a time only
when the remaining answer is user intent. Do not require a fixed number of Q&A
rounds or ask the user to confirm facts the repository settles. Ground every
claim about the existing codebase in
your configured code-intelligence provider's queries when available — the
preflight may have recorded a direct-file-reading fallback in `proposal.md`
`## Grounding`; grounding in real file reads is required either way, guesswork
never is.

When grounding spans more than one area of the codebase, split it into
targeted questions and dispatch an `onto-explorer` per question
**concurrently** — they are read-only, so there is no reason to serialize
them. Synthesize the returns yourself and record what was actually inspected in
`## Grounding`. A subagent cannot ask the user; resolve its technical questions
yourself before deciding that a user question is necessary.

The clarification must end in a summary covering:

- **Goals** — the problem actually being solved, expected outcome
- **Non-goals** — explicitly out of scope
- **Scope boundaries** — modules/users/platforms/data in and out
- **Key unknowns** — open assumptions, risks, dependencies
- **Draft acceptance scenarios** — core success path + important edge cases

Present the summary when it helps the user follow the work, then continue. Ask
for correction only if a material requirement remains unresolved; do not add a
second approval round after the underlying questions are answered.

### 2. Split preflight

If the request spans multiple independent capabilities, journeys, or
milestones — anything that could be designed, built, verified, and closed
independently — propose a split: per item, a name, goals, non-goals,
dependencies, and core scenarios.

If splitting changes delivery order, independently visible scope, or which
capability the user receives, ask whether to split, keep one change, or adjust
the boundary. If the candidates are only technical task decomposition, choose
the smallest cohesive changes yourself and continue.

### 3. Create the workspace

Derive a concise kebab-case name from the request and create
`<workflow-root>/changes/<name>/`; ask about the name only when two plausible names encode
different scope. Create each artifact from its canonical template:

- **Fresh work only:** create the workspace via the binary: `onto new <name>
  --workflow full`
  with one `--repo <declared-alias>` for each configured sibling repository in
  the change's established scope
  (`onto new` creates `onto-state.yaml` carrying `change`, `workflow: full`,
  `phase: open`, `created`; and an empty `proposal.md`. It does **not** scaffold
  `tasks.md` — a full change's task list is derived from the confirmed design in
  onto-design, not written here). Then record the creation fields the same way:
  - `onto set base-ref <name> "$(git rev-parse HEAD)"` — captured NOW, before
    anything is committed; written once, never recomputed.
  - `onto set base-branch <name> "$(git branch --show-current)"` — the branch
    close integrates into, kept separate from the commit-valued base ref. On a
    detached HEAD, derive the intended branch from repository policy.
  - `onto set deps <name> --dep <a> --dep <b>` for each `Depends-on:` entry
    (omit entirely when there are none).
- **Downward-mismatch recovery:** when the dispatcher routed an existing
  workspace here, do not run `onto new` and do not replace its state. Preserve
  its ID, anchors, dependencies, and existing artifacts; recreate only a
  missing `proposal.md` or `notes.md`, then refresh the proposal review token.
- `notes.md` — template: `references/notes.md`. Created NOW, seeded with
  the confirmed clarification summary. From this point, update it before
  ending **any** turn that produced new decisions — this is the
  compaction-recovery checkpoint.
- `proposal.md` — template: `references/proposal.md`; fill the skeleton `onto
  new` created. State the intended scope, non-goals, and acceptance scenarios;
  the concrete task list is **not** written here — it follows from the design.

Everything in the proposal must trace back to the confirmed clarification
summary — no invented scope.

Review the proposal against the request, clarification summary, and grounding.
Fix any mismatch, summarize the resulting scope, then record the review token;
the binary refuses to leave open without it:

```
onto set proposal-approved <name> "YYYY-MM-DD <one-line review summary>"
```

## Exit checklist

- [ ] Workspace exists with `onto-state.yaml`, `notes.md`, `proposal.md`,
      all template-conformant and consistent with the confirmed summary
      (no `tasks.md` yet — it is derived in design)
- [ ] `notes.md` Confirmed section reflects every settled decision
- [ ] `proposal.md` `## Grounding` is filled — the queries run, or the
      recorded fallback if the provider was unavailable/declined; never left
      blank (the close lint blocks a blank Grounding at archive)
- [ ] Every material intent question is resolved; any split decision that
      changed user-visible delivery is recorded
- [ ] `onto set proposal-approved <name> "<evidence>"` recorded — `onto
      advance` refuses to leave open without it
- [ ] If recorded phase is open, advanced open → design via `onto advance
      <name>` after review; on a downward mismatch, skipped advance and handed
      directly to `onto-design` so the unchanged later phase cannot route back
      into open
- [ ] onto-no-slop pass run over `proposal.md` and `notes.md`, the pass
      recorded in `notes.md` (`no-slop: <artifact> done`)
- [ ] **Commit the workspace**: `git add <workflow-root>/changes/<name> && git commit`
      — every phase exits with its workspace committed; state recovery,
      `base_ref` rebuild, and the close-phase `git mv` all depend on the
      workspace being tracked
- [ ] Load `onto-design` and continue in the same invocation unless the user
      named open as the endpoint or asked to pause
