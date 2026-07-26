# KISS — keep it simple

The simplest thing that meets the requirement wins. Complexity must buy its
way in with a named, present need. In an agent workflow it is doubly
expensive, because every future session pays the comprehension cost fresh.
Both frameworks encode KISS in their mechanics; this guide maps where.

Simplicity is not the same as brevity. Explicit, boring, one-step-at-a-time
code is KISS; a dense one-liner that needs a comment to decode is not. When
in doubt, optimize for the reader who arrives with zero context. In these
workflows that reader is usually a fresh agent session.

## KISS in onto

- **The simplest design that meets the requirements.** The design phase's job
  is to pick an approach and record *why not* the alternatives — an ADR
  documenting declined complexity is a KISS artifact. A design whose diagram
  needs a diagram is a signal to split the change.
- **Bite-sized tasks, one commit each.** ~200 lines per reviewable commit is
  a complexity budget: anything bigger gets split. One outcome per task; no
  "and" in a task title.
- **Boring machinery over clever machinery.** onto's own conventions model
  this: state is a flat YAML file, phase derivation greps literal markers
  (`Status: Confirmed`, `- [ ]`), gates are exact string matches. Prefer the
  same style in your changes — grep-able literals over indirection.
- **Reviews propose the smallest fix.** `onto-reviewer` is instructed to
  propose the minimal change per finding, not a rewrite. Accepting a
  finding never means gold-plating the surrounding code.

## KISS in to

- **Plans under a screen.** The `to-plan` rule is explicit: a plan nobody
  reads is ceremony. Goal, boundary, a handful of task contracts, one
  `Final Verify:` line — if it doesn't fit, the change is too big or the
  prose is too clever.
- **One artifact.** Planning, notes, review outcomes, and verification
  evidence all live in `plan.md`. Resist inventing sidecar documents; the
  contract exists so one file survives archiving as the whole story.
- **One writer at a time.** A single implementer holds the working tree;
  read-only agents (explorer, reviewer, skeptic) run as concurrently as the
  work justifies. Simplicity here means one tree and one bookkeeper, not one
  agent — worktree-per-implementer orchestration is onto-scale machinery.
- **Match the surrounding code.** The code-writing standards bind every
  task: read first, match style, naming, idiom, and comment density. A
  locally-clever pattern that fights the codebase's conventions is
  complexity, even at three lines.

## KISS when writing the code itself

1. **Solve it the obvious way first.** Reach for the standard library, the
   existing helper, the pattern the neighboring file already uses. Novelty
   needs a reason you can state in the commit message.
2. **Flat over nested, explicit over implicit.** Early returns beat arrow
   pyramids; a named intermediate beats a chained expression; duplication
   twice is cheaper than the wrong abstraction once.
3. **Comments state constraints, not narration.** If the code needs a
   paragraph to explain *what* it does, simplify the code instead.
4. **Simple prose too.** The no-slop skills (`onto-no-slop` / `to-no-slop`)
   are KISS for writing: active voice, named actors, no filler — the same
   rules, applied to the artifacts a human keeps.

The test, always: **could a fresh session extend this without reading twice?**
If not, it isn't done. Simplicity is part of the task's outcome, not polish
for later.

See also: [YAGNI](yagni.md) — YAGNI bounds *what* you build; KISS bounds *how*.
