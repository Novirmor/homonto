# Systematic debugging protocol

On ANY build failure, test failure, or unexpected behavior during build: **stop
and find the root cause before proposing any fix.** A symptom patch is a failure
— it hides the bug and often spawns new ones.

## The iron law

**No fix before the root cause is identified.** If you have not completed the
investigation below, you may not propose or apply a fix.

## The four phases

**1. Root-cause investigation (do this first, always).**
- **Reproduce** it reliably — the exact command and conditions.
- **Read the whole error** — the full message and stack, not the first line.
- **Check recent changes** — what did this change touch? `git diff`, the last
  commits.
- **Trace the data flow** — follow the actual values to where the wrong one is
  produced. Fix at the source, not where the symptom surfaced.

**2. Pattern analysis.** Is this one bug or an instance of a class? Does the same
mistake exist elsewhere?

**3. Hypothesis and test.** State it explicitly: "I think X is the root cause
because Y." Make the **smallest** change that would confirm or refute it. Worked
→ phase 4. Didn't → form a *new* hypothesis (do not pile changes on).

**4. Implementation.** Fix the root cause. If it is a source bug, add a **minimal
failing test that reproduces it** first (TDD protocol), then fix, then watch the
test pass, then run the surrounding suite.

## Escalation

After **3 failed hypotheses**, stop patching: the problem is likely the
architecture or a wrong assumption, not the line you keep editing. Surface it,
re-analyze from phase 1 with the new information, and use fresh exploration or a
reviewer rather than a fourth guess. Ask the user only if the rethink changes
the agreed behavior, scope, compatibility, cost, or another constraint they own.

## Red flags — stop and return to phase 1

Changing code to "see if it helps" · fixing where the error printed instead of
where the value went wrong · "it's probably X" without reproducing · multiple
simultaneous changes · "let me just try…". All mean: you skipped the
investigation. Go back to phase 1.
