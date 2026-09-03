---
name: grilling
description: Grill the user relentlessly about a plan or design to stress-test it before building. Use when the user wants to pressure-test an approach, or uses a 'grill' trigger phrase.
metadata:
  source: adapted from https://github.com/mattpocock/skills (MIT), skills/productivity/grilling
---

# grilling

Interview the user about every consequential part of this plan until you both
reach a shared understanding. Walk the design as a tree: settle an upstream
decision before the choices that depend on it, so no answer gets invalidated by
a later one.

## How to ask

- **One question at a time.** Ask, wait for the answer, then ask the next.
  Several questions at once is bewildering and produces shallow answers.
- **Recommend an answer.** For each question, state your recommended choice, the
  one reason it wins, and the main trade-off. A blank question offloads the
  thinking back onto the user.
- **Look up facts; ask for decisions.** If the codebase settles it (which
  function, what the current behavior is, whether a file exists), read it —
  don't ask. Put the real choices to the user: trade-offs, priorities, scope,
  naming, anything with no single right answer.
- **Go deepest where it's riskiest.** Spend questions on the decisions that are
  expensive to reverse or that everything downstream rests on. Skip the ones
  that are cheap to change later, and say you're skipping them.

## Track and close

- Keep a running list of settled decisions as you go, so a context reset can't
  lose them and the user can see the shape forming.
- Stop when the open questions left are low-stakes or the user calls it. Don't
  manufacture questions to look thorough.
- End by replaying every decision as a short numbered summary and ask only for
  corrections to a material misunderstanding. Do not enact the plan; this skill
  ends with the decision record.
