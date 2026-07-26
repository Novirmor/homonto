# Brainstorm protocol (design phase)

Deep design is not optional and it is not "write the design doc." It is a
disciplined exploration that must happen **before** `design.md` is written, and
it ends in an approach the user explicitly confirmed. This is what separates the
full workflow from the presets.

## Anti-pattern: "this is too simple to design"

Every full change goes through this. Truly simple changes get a short design (a
few sentences) — but you still explore and still get approval. "Simple" is
exactly where an unexamined assumption wastes the most work; if it is genuinely
that simple, it was probably a `tweak`.

## The process

**1. Ground the context.** Read the real code the change touches (your
configured code-intelligence provider, or direct reads — record which).
Never design against a guess.

**2. Clarify — one question at a time.** Ask until purpose, constraints, and
success criteria are unambiguous. **One question per message** (prefer multiple
choice); if a topic is deep, break it into several questions rather than stacking
them. Do not treat a single round as enough. If the request actually describes
several independent subsystems, stop and flag a split (that is `onto-open`'s
job), don't refine the details of something that should be decomposed.

**3. Propose 2–3 approaches.** For the core design decisions, present **two or
three distinct approaches with their trade-offs**, lead with your recommendation
and *why*. Not one option presented as fait accompli, not a menu with no
opinion — real alternatives and a reasoned pick.

> **GATE (approach confirmation):** the user chooses (or adjusts) the approach.
> **Do not write `design.md` until this is answered** — a designed-around-the-
> wrong-approach doc is expensive to unwind. Always fresh input.

**4. Only then write it.** Record the confirmed approach in `notes.md`, then
write `design.md` (`Status: Confirmed` + date), the ADR drafts for each
significant decision, and the delta-spec scenarios — and derive `tasks.md` from
the confirmed approach.

## Checkpoint

Update `notes.md` incrementally as the brainstorm progresses (each clarified
point, each approach considered), marking unconfirmed items as candidates. This
is the compaction-recovery checkpoint — a design conversation lost to context
compaction is otherwise gone.

## Discipline

- Apply **YAGNI**: design for the confirmed requirement, not imagined future
  ones. A "professional-grade" feature nobody asked for is scope you must justify
  or cut.
- Listen to friction: if the approach is hard to design cleanly, that is the
  design telling you something — reconsider, don't push through.
