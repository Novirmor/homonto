# TDD protocol (`tdd-mode: tdd`)

The rule is one line; the value is the defenses against talking yourself out of
it. onto-fix mandates `tdd-mode: tdd` (a fix's whole method is a failing test
that reproduces the bug), and any change with testable logic runs it.

## The iron law

**No production code without a failing test first.**

Wrote code before the test? **Delete it and start over from the test.** Not "keep
it as reference", not "adapt it while I write the test", not "look at it" — delete
means delete. Tests written against code you already wrote only ask "what does
this do?"; tests written first ask "what should this do?"

## Red → Green → Refactor

1. **RED** — write the smallest test that expresses the next required behavior.
2. **Verify RED** — run it and **watch it fail for the expected reason**. A test
   that passes immediately, or fails for the wrong reason, proves nothing —
   fix the test until it fails correctly.
3. **GREEN** — write the *minimal* code to pass it. No extra cases, no
   speculative generality.
4. **Verify GREEN** — run it and watch it pass. Run the surrounding suite.
5. **REFACTOR** — clean up with the test as your safety net. Then the next test.

## Rationalizations — each means "write the test first"

| Excuse | Reality |
|---|---|
| "Too simple to test" | Simple code still breaks; the test is 30 seconds. |
| "I'll test after" | Tests that pass on first run prove nothing. |
| "Already manually tested" | Ad-hoc ≠ systematic; no record, can't re-run. |
| "Deleting my work is wasteful" | Sunk cost. Unverified code is the debt. |
| "Keep it as reference" | You'll adapt it — that's testing after. Delete. |
| "Hard to test" | Listen to the test: hard to test = hard to use; fix the design. |
| "TDD will slow me down" | TDD is faster than the debugging it prevents. |

## Red flags — stop and start over

Code before test · test after implementation · test passes immediately · can't
explain why it failed · "just this once" · "it's the spirit not the ritual" ·
"this case is different because…". All of them mean: delete the code, restart
from the test.

## Not TDD

Content/config/docs deliverables with no testable logic run `tdd-mode: direct`
(implement, then run the task's stated verification) — recorded as such at the
plan review, not changed silently mid-task.
