---
name: onto-design
description: onto phase 2 — deep design. Use when an active full-workflow change has phase design — explores alternatives, selects an approach from evidence and user-owned constraints, then writes design.md, ADR drafts, spec deltas, and tasks.md.
---

# onto-design — Phase 2: Deep Design

Produce a confirmed technical design before any implementation exists.
**Design cannot be skipped in the full workflow** — this phase is the reason
the full workflow exists.
Apply the dispatcher's shared autonomous workflow policy throughout.

## Entry check

- `onto state <name> --json` reports `workflow: full`, `proposal.md` exists and
  was reviewed in open, and either recorded phase or dispatcher-routed derived
  phase is `design`.
- Read `notes.md` first (create it from `onto-open/references/notes.md` if
  missing) — resume from Pending; never re-ask Confirmed. After
  compaction, notes.md is the *why*-recovery; the derivation table is the
  *where*-recovery.
- Presets never enter this phase — except a preset **upgraded** to full,
  which arrives here to backfill the design it skipped.
- **Revision entry**: if `design.md` exists marked `Status: Under
  revision`, this is a mid-build design revisit. Re-evaluate the approach for
  the revised scope. Ask the user only if the revision introduces a new
  product-level trade-off; otherwise select the best technical approach and
  record the decision. Write a fresh `Status: Confirmed` + date and update
  notes.md; the change then resumes build.
- **Downward mismatch entry**: the recorded phase may already be build, verify,
  or close. Repair and confirm the design, but do not advance the recorded phase;
  return through `/onto` so artifact derivation selects the next missing phase.
- Any other state → route back through `/onto`.

## Steps

Steps 1–3 are the **brainstorm** — follow `references/brainstorm-protocol.md` for
the discipline behind them: why "too simple to design" is a trap, focused
questions only for missing intent, real alternatives, YAGNI, and the incremental
`notes.md` checkpoint. The design doc is written only after an approach is
selected and recorded.

### 1. Explore ground truth

Before proposing anything, read the real system: your configured
code-intelligence provider's queries for structure and call paths when
available (the preflight may have recorded a direct-file-reading fallback),
then the actual files. Map the
integration points the proposal touches. Never design against an imagined
codebase.

### 2. Question until clear

If goals, scope, constraints, or acceptance scenarios still have gaps, keep
asking — one question at a time. Do not write a design around an unresolved
unknown; resolve it or explicitly record it as a risk with a fallback.

### 3. Evaluate 2–3 approaches

Develop genuinely different candidate approaches with trade-offs and a
recommendation. Record them in `notes.md`. When they are equivalent from the
user's perspective, select the recommended technical approach and continue.
Present a choice only when the alternatives materially change product behavior,
compatibility, security posture, cost, or another user-owned constraint.

**Parallel exploration:** when the approaches are genuinely open and
substantial, dispatch 2–3 fresh-context agents, each
developing one approach sketch (architecture, key risks, effort) in
parallel; the main session synthesizes the comparison and owns the decision.

Update `notes.md` after every clarification round and approach iteration —
before ending the turn, not after.

Record the selected approach before writing the final design. The binary refuses
design → build without this evidence token:

```
onto set approach-confirmed <name> "YYYY-MM-DD <chosen approach and basis>"
```

**No implementation code in this phase.** Writing source code before a
confirmed design exists is prohibited, regardless of how simple the change
looks.

### 4. Write the design artifacts

After selection, write into the workspace, each from its canonical
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
`<workflow-root>/changes/<name>/tasks.md` from the confirmed design (template:
`onto-open/references/tasks.md`). This is the right time: tasks flow *from* the
design, not before it. Each task is a bite-sized, independently verifiable unit
tracing to a design decision or delta scenario; build refines granularity but
the boundaries come from here. `tasks.md` does not exist until this step (onto
new no longer scaffolds it for a full change), and leaving design requires it —
so a design that produced no task list is not done.

Write every new task as `- [ ] N.M <outcome> [trace #K]`: `N.M` pairs with its
future plan heading, while positive unique `K` is the evidence/trace key. Use
the canonical template; do not create new legacy leading `#K` tasks.

## Isolation decision (before leaving design)

The binary refuses to advance design → build without isolation. Choose and
record it without asking: `branch` for a clean, serial change off the base ref;
`worktree` for parallel changes, an unrelated dirty current tree, or concurrent
work. Run `onto set isolation <name> branch|worktree` before `onto advance`.
Never stash, overwrite, or absorb unattributed work to make the preferred choice
fit.

`build-mode` and `tdd-mode` are build-phase decisions (see `onto-build`).

## Exit checklist

- [ ] `design.md` exists, marked `Status: Confirmed` with date, and matches
      the selected approach and any user-owned constraints
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
- [ ] `design.md` `## Grounding` is filled — the code-intelligence
      queries and file reads the design rests on, or the recorded fallback;
      never blank
- [ ] `notes.md` records the confirmed approach and every decision made
- [ ] onto-no-slop pass run over `design.md`, every ADR draft, and
      `notes.md`, each pass recorded in `notes.md` (`no-slop: <artifact>
      done`)
- [ ] Isolation chosen via `onto set isolation <name> branch|worktree` and
      the approach token recorded via `onto set approach-confirmed <name>
      "<evidence>"` — the binary refuses design → build without both
- [ ] If recorded phase is design, advanced design → build via `onto advance
      <name>`; on a downward mismatch, skipped advance and returned to `/onto`
- [ ] **Commit the workspace**: `git add <workflow-root>/changes/<name> && git commit`
      — every phase exits with its workspace committed
- [ ] Load `onto-build` and continue in the same invocation unless the user
      named design as the endpoint or asked to pause
