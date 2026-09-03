# Brainstorm protocol (design phase)

Deep design is not optional and it is not "write the design doc." It is a
disciplined exploration that must happen **before** `design.md` is written, and
it ends in an approach selected from evidence and user-owned constraints. This is what separates the
full workflow from the presets.

## Anti-pattern: "this is too simple to design"

Every full change goes through this. Truly simple changes get a short design (a
few sentences), but you still explore and record the selected approach. "Simple" is
exactly where an unexamined assumption wastes the most work; if it is genuinely
that simple, it was probably a `tweak`.

## The process

**1. Ground the context.** Read the real code the change touches (your
configured code-intelligence provider, or direct reads — record which).
Never design against a guess.

**2. Clarify missing intent.** Resolve technical facts through repository
evidence. If purpose, constraints, or success criteria remain ambiguous, ask one
focused question at a time. Do not require a fixed number of rounds. If the request actually describes
several independent subsystems, stop and flag a split (that is `onto-open`'s
job), don't refine the details of something that should be decomposed.

**3. Evaluate 2–3 approaches.** For the core design decisions, develop two or
three distinct approaches with their trade-offs, lead with your recommendation
and why. Choose it when the differences are technical and reversible. Ask the
user only when the alternatives materially change user-visible behavior, scope,
compatibility, security posture, cost, or another constraint they own.

**4. Only then write it.** Record the selected approach in `notes.md`, then
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
