# Writing an ADR

With workflow artifacts untracked, the ADR is the durable record of a change.
It exists to stop the same argument being had twice.

## When one is owed

Write an ADR when the work made a decision a reasonable person could later
question, and the code alone will not explain why:

- A structural or architectural choice, or reversing an earlier one
- A breaking change, or a deliberate refusal to provide compatibility
- Choosing between real alternatives, where the loser was defensible
- Deciding **not** to do something, when the absence will look like an oversight
- A cross-cutting policy — how errors surface, what gets committed, what ships

Do not write one for a bug fix, a rename, a dependency bump, added tests, or
anything the diff already explains. Padding `docs/adr/` with non-decisions makes
the real decisions harder to find.

If you are unsure: would someone six months from now read the code and ask "why
on earth is it like this"? That question is the test.

## Format

Keep it short. Most ADRs here should fit on one screen. The headings are fixed
so the directory stays skimmable; the prose under them should be as brief as
the decision allows.

```markdown
# <Imperative title: "Adopt X", "Stop committing Y">

- **Status:** Accepted
- **Date:** YYYY-MM-DD

## Context

What forced a decision. The constraint or failure, not a history lesson.

## Decision

We will <the decision, stated plainly>.

## Consequences

What this costs and what it enables. Include the bad parts — an ADR listing
only benefits is marketing, and the cost is the part future readers need.
```

## Numbering

Four digits, zero-padded, strictly increasing: next number = highest in
`docs/adr/` + 1. Numbers are never reused. A superseded ADR keeps its file and
its Status becomes `Superseded by NNNN`; it is never deleted or edited to match
the new decision, because the record of having changed course is the value.

Number it when you write it. The old rule — draft unnumbered inside a change,
assign the number at archive — existed to avoid collisions between parallel
OpenSpec changes and no longer applies.

## Style

Write for someone who does not have the context you have right now. Name the
alternative you rejected and why. State the cost honestly. Avoid words that
carry no information: "robust", "seamless", "leverage", "best practice".
