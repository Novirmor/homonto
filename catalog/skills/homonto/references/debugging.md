# Systematic debugging protocol

Use for a reported bug or unexpected build/test failure in either workflow,
including presets and delegated implementation. Investigate before applying a
fix: changing the symptom can hide the cause and introduce another failure.
An expected red test in a test-first task is not an unexpected failure.

## The four phases

**1. Root-cause investigation.**
- **Reproduce** it reliably — the exact command and conditions.
  If reproduction is unreliable, gather evidence about timing, inputs and
  environment before patching; distinguish observations from assumptions.
- **Read the whole error** — the full message and stack, not the first line.
- **Check recent changes** — what did this change touch? `git diff`, the last
  commits.
- **Trace the data flow** — follow the actual values to where the wrong one is
   produced. Fix at the source, not where the symptom surfaced.
- **Inspect component boundaries** when several components are involved: compare
  inputs, outputs and configuration at the failing boundary without exposing secrets.

**2. Pattern analysis.** Compare the failing path with a working path in this
repository, including dependencies and configuration. Is this one bug or an
instance of a class? Investigation may reveal related defects; report those
outside the assigned scope rather than silently fixing them.

**3. Hypothesis and test.** State it explicitly: "I think X is the root cause
because Y." Test one variable at a time with the smallest experiment that would
confirm or refute it. Worked → phase 4. Didn't → form a new hypothesis; do not
pile speculative edits on top or discard pre-existing work to reset an experiment.

**4. Implementation.** Fix the root cause. If it is a source bug, add a **minimal
failing test that reproduces it** first (TDD protocol), then fix, then watch the
test pass, then run the surrounding suite. Report evidence using the shared
[candidate-bound verification policy](execution.md#candidate-bound-verification).

## Escalation

After **3 failed hypotheses**, stop patching: the problem is likely the
architecture or a wrong assumption, not the line you keep editing. Surface it,
re-analyze from phase 1 with the new information, and use fresh exploration or a
reviewer rather than a fourth guess. Ask the user only if the rethink changes
the agreed behavior, scope, compatibility, cost, or another constraint they own.
A worker returns scope decisions or unavailable evidence to its coordinator;
it does not spawn reviewers or ask the user. If bounded investigation cannot
establish the cause, report the evidence, remaining uncertainty and next needed
probe instead of claiming a fix or continuing speculative patches.

## Red flags — stop and return to phase 1

Changing code to "see if it helps" · fixing where the error printed instead of
where the value went wrong · "it's probably X" without reproducing · multiple
simultaneous changes · "let me just try…". All mean: you skipped the
investigation. Go back to phase 1.
