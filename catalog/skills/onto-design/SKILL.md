---
name: onto-design
description: onto phase 2 — deep design. Use when an active full-workflow change has phase design — brainstorming-grade exploration, approach confirmation, then design.md plus ADR drafts and spec deltas.
---

# onto-design — Phase 2: Deep Design

Produce a confirmed technical design before any implementation exists.
**Design cannot be skipped in the full workflow** — this phase is the reason
the full workflow exists.

## Entry check

- `onto-state.yaml` has `phase: design` and `workflow: full`; `proposal.md`
  exists and was approved in open.
- Read `notes.md` first (create it from `onto-open/references/notes.md` if
  missing) — resume from Pending; never re-ask Confirmed. After
  compaction, notes.md is the *why*-recovery; the derivation table is the
  *where*-recovery.
- Presets never enter this phase — except a preset **upgraded** to full,
  which arrives here to backfill the design it skipped.
- **Revision entry**: if `design.md` exists marked `Status: Under
  revision`, this is a mid-build design revisit — the approach gate is
  re-asked **for the revised scope regardless of what notes.md Confirmed
  records** (the old approach answer does not cover the new scope; this
  overrides "never re-ask Confirmed" for exactly this gate).
  Re-confirmation writes a fresh `Status: Confirmed` + date and records
  the new answer in notes.md; the change then resumes build.
- Any other state → route back through `/onto`.

## Steps

Steps 1–3 are the **brainstorm** — follow `references/brainstorm-protocol.md` for
the discipline behind them: why "too simple to design" is a trap, one question
per message, real alternatives (not a fait accompli), YAGNI, and the incremental
`notes.md` checkpoint. The design doc is written only *after* the approach gate.

### 1. Explore ground truth

Before proposing anything, read the real system: graphify/codegraph queries
for structure and call paths when available (the preflight may have
recorded a direct-file-reading fallback), then the actual files. Map the
integration points the proposal touches. Never design against an imagined
codebase.

### 2. Question until clear

If goals, scope, constraints, or acceptance scenarios still have gaps, keep
asking — one question at a time. Do not write a design around an unresolved
unknown; resolve it or explicitly record it as a risk with a fallback.

### 3. Propose 2–3 approaches

Present genuinely different candidate approaches with trade-offs and a
recommendation. Lead with the recommended one and say why. Record the
candidates in `notes.md` before presenting.

**Parallel exploration (optional):** when the approaches are genuinely
open and substantial, you MAY dispatch 2–3 fresh-context agents, each
developing one approach sketch (architecture, key risks, effort) in
parallel; the main session synthesizes and still presents the comparison
itself. Never dispatch agents to make the choice — the gate below is the
user's.

Update `notes.md` after every clarification round and approach iteration —
before ending the turn, not after.

> **GATE (approach confirmation):** the user picks or adjusts an approach.
> The final `design.md` MUST NOT be written before this gate is answered.
> Always fresh input — a blanket directive does not pre-answer it.

**No implementation code in this phase.** Writing source code before a
confirmed design exists is prohibited, regardless of how simple the change
looks.

### 4. Write the design artifacts

After confirmation, write into the workspace, each from its canonical
template in this skill's `references/`:

- `design.md` — template: `references/design.md`. `Status: Confirmed` +
  `Confirmed: <date>` are the lines the phase derivation keys on.
- `adr/<slug>.md` — template: `references/adr-draft.md`; one draft per
  significant decision, `Status: Proposed`, **unnumbered** (numbers at
  close).
- `specs/<capability>.md` — template: `references/delta-spec.md`
  (ADDED/MODIFIED/REMOVED/RENAMED sections, SHALL first lines,
  GIVEN/WHEN/THEN scenarios — the close-phase lint enforces exactly that
  template's rules). Every behavior change needs a scenario; deltas stay
  living documents until close merges them.

Mark the confirmed approach in `notes.md`.

**Derive the task list.** Now — with the approach confirmed — write
`docs/changes/<name>/tasks.md` from the confirmed design (template:
`onto-open/references/tasks.md`). This is the right time: tasks flow *from* the
design, not before it. Each task is a bite-sized, independently verifiable unit
tracing to a design decision or delta scenario; build refines granularity but
the boundaries come from here. `tasks.md` does not exist until this step (onto
new no longer scaffolds it for a full change), and leaving design requires it —
so a design that produced no task list is not done.

## Isolation gate (before leaving design)

> **GATE (isolation):** the binary refuses to advance design → build unless an
> isolation is chosen, so build work is never committed unisolated. Ask now,
> before `onto advance`:
>
> - `onto set isolation <name> branch|worktree` — branch for a simple change
>   off the base ref; worktree for parallel changes or a dirty current branch
>
> This gate MAY be pre-authorized: if the user gave an explicit directive
> covering isolation (e.g. "run to completion with defaults"), record it
> **verbatim** via `onto set directive <name> "<text>"` and proceed with
> `branch` as the default. When in doubt, ask.
>
> `build-mode` and `tdd-mode` are NOT asked here — they are build-phase
> decisions tied to the plan-ready gate (see `onto-build`). Only isolation
> gates the advance.

## Exit checklist

- [ ] `design.md` exists, marked `Status: Confirmed` with date, and matches
      the user-confirmed approach
- [ ] An ADR draft exists for every significant decision named in design.md
      — **enumerate them**: list each Key decision from design.md next to
      its `adr/<slug>.md` file; a decision with no draft is a gap to fix
      now, not a checkbox to tick
- [ ] A delta spec scenario exists for every behavior change — **enumerate
      them**: list each behavior change design.md names next to its
      `specs/<capability>.md` scenario; the close-phase lint re-checks the
      diff against declared deltas, so a skipped delta surfaces there as a
      blocking finding
- [ ] `tasks.md` written from the confirmed design — bite-sized tasks, each
      tracing to a decision or delta scenario (required to leave design)
- [ ] No implementation code was written
- [ ] `design.md` `## Grounding` is filled — the graphify/codegraph
      queries and file reads the design rests on, or the recorded fallback;
      never blank
- [ ] `notes.md` records the confirmed approach and every decision made
- [ ] onto-no-slop pass run over `design.md`, every ADR draft, and
      `notes.md`, each pass recorded in `notes.md` (`no-slop: <artifact>
      done`)
- [ ] Isolation chosen via `onto set isolation <name> branch|worktree` — the
      binary refuses design → build without it
- [ ] Phase advanced design → build via `onto advance <name>`
- [ ] **Commit the workspace**: `git add docs/changes/<name> && git commit`
      — every phase exits with its workspace committed
- [ ] Announce the transition and load `onto-build`
