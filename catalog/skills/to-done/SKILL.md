---
name: to-done
description: to phase 3 — done. Use when a change's plan is fully executed — runs real verification, obtains one completed skeptic pass on the final candidate, records the outcome, then `to done --verified` archives the change.
---

# to-done — Phase 3: Done

Finish honestly. The binary will accept `--verified` from anyone; this skill
is what makes the assertion true before it is made.

## Entry check

- `to status --json` shows the change at `phase: do` with every plan task
  checked.
- The working tree is committed (the binary is git-blind and won't check —
  you do).

## Steps

1. **Run the plan's `Final Verify:` command**, not a task's nested check, and
   read the output. State honestly what it covered; record any unavailable or
   skipped checks as gaps rather than treating one green command as universal
   proof.
2. **Obtain at least one completed `to-skeptic` pass on the final candidate.**
   Hand it the complete `plan.md` (including `## Notes`) and the claim being
   made ("this change works because …"). Skeptics are read-only, so dispatch
   several concurrently when the change warrants it, each with a distinct lens
   (does it do what it claims / what breaks it / does the evidence hold). One
   completed pass is the floor, not the ceiling.
   - A skeptic attempt that returns a blocking `Questions:` section is
     incomplete. Resolve the question, then re-dispatch against the same
     candidate.
   - If accepted findings change code, the previous verdict describes an old
     tree. Re-run `Final Verify:`, then re-dispatch once against the new final
     candidate. Keep only the completed verdict for the tree being archived.
3. **Triage its findings.** Fix what's real (back through the `to-do` loop for
   anything substantial), decline the rest with a written reason. A code change
   invalidates both the previous `Final Verify:` result and skeptic verdict;
   repeat steps 1–2 before finishing.
4. **Record the outcome** under `## Verification` at the bottom of `plan.md`:
   the literal verify command and result, coverage gaps or skipped checks, and
   the skeptic's verdict (including declined findings). De-slop the prose, but
   do not commit yet.
5. **Assert and archive:**
   `to done <name> --verified --evidence "<the literal verify command and its result>"`.
   The evidence string is recorded verbatim in the archived state — it is
   what makes this verification distinguishable from a skipped one later.
   The change moves to `docs/tasks/archive/<date>-<name>/`.
6. **Commit the archived result.** Commit the `## Verification` record,
   terminal `to-state.yaml`, and directory move together. The archive is the
   durable review artifact; do not leave it as uncommitted cleanup after a
   pre-archive commit.

## Rules

- **Never pass `--verified` before steps 1–4 are done.** The checkbox is
  self-asserted by design; asserting it without the work is lying in writing,
  in a reviewable artifact.
- If verification fails and the fix isn't obvious, stay in `do` — tell the
  user rather than force the finish.
- Never hand-edit `to-state.yaml`; archiving is the binary's move, not `mv`.
