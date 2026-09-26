# TDD protocol (`tdd-mode: tdd`)

New tasks with testable behavior start with a failing test. `onto-fix` always
retains this reproduction requirement. Content/config/docs with no testable logic
use the recorded direct mode and their stated verification instead.

## Red → green → refactor

1. Derive the smallest test from the required behavior, not implementation details.
2. Run it against the unfixed candidate and observe failure for the expected
   reason. An unrelated setup error is not a reproduction. Keep literal evidence.
3. Implement the smallest in-scope fix and observe the same test pass.
4. Run the relevant surrounding checks. Refactor only within the assigned scope,
   retaining passing evidence for the resulting candidate.

## Interrupted or preexisting implementation

If implementation predates the test, preserve the work and record the sequence
honestly. Derive the test from acceptance criteria, demonstrate the expected
failure against the pre-change candidate in an authorized fixture, then demonstrate
the fix on the current candidate. Follow the shared workspace policy for fixture
scope and dirty input. Never reset/delete user or interrupted work to manufacture
a test-first history. An unavailable reproduction is an evidence gap to resolve,
not permission to claim a red run. New fix tasks still start with red.

If a test passes before the fix, investigate whether it exercises the claimed
behavior. If a faithful reproduction needs unavailable infrastructure, record the
blocker rather than silently switching a fix to direct mode. Ask through the
coordinator's built-in `question` tool only when a user-owned decision is needed.
