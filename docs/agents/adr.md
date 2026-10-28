# Writing an ADR

Workflow artifacts are not committed here, so an ADR is the only durable
record of a decision. It exists to stop the same argument from being had
twice. Everything else a change leaves behind is its commit and its tests.

## When one is owed

Write an ADR when the work made a decision a reasonable person could later
question and the code alone will not explain why:

- a structural or architectural choice, or reversing an earlier one;
- a breaking change, or a deliberate refusal to provide compatibility;
- a choice between real alternatives, where the rejected one was defensible;
- deciding **not** to do something, when the absence will look like an
  oversight;
- a cross-cutting policy: how errors surface, what gets committed, what ships,
  what an agent may do without asking.

Do not write one for a bug fix, a rename, a dependency bump, added tests, or
anything the diff already explains. Non-decisions in `docs/adr/` make the real
ones harder to find.

The test: would someone six months from now read the code and ask "why on
earth is it like this"? If yes, write it.

## Format

Most ADRs fit on one screen. The headings are fixed so the directory stays
skimmable; the prose under them is as short as the decision allows.

```markdown
# <Imperative title: "Adopt X", "Stop committing Y">

- **Status:** Accepted
- **Date:** YYYY-MM-DD

## Context

What forced a decision: the constraint or failure, not a history lesson.

## Decision

We will <the decision, stated plainly>.

## Consequences

What this costs and what it enables. Include the bad parts. The cost is
what a future reader needs most.
```

## Numbering and superseding

- Four digits, zero-padded. The next number is the highest in `docs/adr/`
  plus one, assigned when you write the ADR. Numbers are never reused.
- To change course, write a new ADR. Set the old one's Status to
  `Superseded by NNNN`, or `Partially superseded by NNNN (what)` when only part
  of it changed. Never delete it or rewrite its decision to match: the record
  of having changed course is the point.
- Add the new ADR to the index in [`docs/adr/README.md`](../adr/README.md), and
  update the Status column of any ADR it supersedes.

## Style

Write for someone who lacks the context you have right now. Name the
alternative you rejected and why. State the cost honestly. Skip words that
carry no information: "robust", "seamless", "leverage", "best practice".
