# Receiving review — verify before you implement

When the `onto-reviewer` subagent (or a human, or a PR reviewer) returns
findings, they are **input to evaluate, not instructions to execute**. A review
loop that implements every finding blindly ships the reviewer's mistakes too —
and since v0.1.3 wired the reviewer into build, this discipline is load-bearing.

Core principle: **verify before implementing; investigate before asking;
technical correctness over agreement.**

## The response pattern (per finding)

1. **Read** the whole finding without reacting.
2. **Understand** — restate the claim in your own words; if you can't, it's
   unclear (see below).
3. **Verify** — check the claim against the actual code. Does the failure
   scenario it describes really occur? Read the surrounding code the reviewer may
   not have seen.
4. **Evaluate** — is it correct *for this codebase*? Does it break existing
   behavior? Is there a reason the current code is the way it is?
5. **Respond** — a technical acknowledgment, or **reasoned pushback with
   evidence** if the finding is wrong.
6. **Implement** — one finding at a time, verify each (the task's test/build),
   before the next.

## Forbidden

- Performative agreement — "you're absolutely right", "great catch" — then
  implementing. Say nothing; act, or push back with reasoning.
- Implementing before verifying. A plausible-sounding finding can still be wrong
  for this code.
- Implementing a **subset** when some findings are unclear: items may be related,
  and partial understanding produces a wrong change. Investigate unclear items
  first; ask the user only when product intent is missing.

## When a finding is wrong

Push back with the specific technical reason and the code that refutes it — do
not silently drop it (the reviewer or user may know something you don't) and do
not implement it. If you cannot verify it either way, name the missing evidence,
gather it, and re-evaluate before proceeding.

## Severity, in the onto build loop

The reviewer already ranks findings. **CRITICAL** (correctness, security, a
failed core scenario) is fixed via a re-dispatched `onto-implementer` before the
next task — *after* you've verified it is real. Non-critical findings you accept
are recorded in the plan or the commit body; ones you reject are noted with the
reason. When severity is unclear, do not inflate it.
