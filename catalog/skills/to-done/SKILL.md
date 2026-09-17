---
name: to-done
description: to phase 3 — done. Use when a change's plan is fully executed — runs real verification, obtains one completed skeptic pass on the final candidate, records the outcome, then `to done --verified` archives the change.
---

# to-done — Phase 3: Done

Finish honestly. The binary will accept `--verified` from anyone; this skill
is what makes the assertion true before it is made.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry.

## Entry check

- `to status --json` shows the change at `phase: do` with every plan task
  checked.
- Commit only verified assigned implementation changes in each selected source
  execution root. Schema 2 has no implicit config repo; configRoot may be
  non-Git. The clean gate uses registered bindings where present, not arbitrary
  clean checkouts. Inspect dirt and honor the existing preserve/isolate/cleanup
  decision; preserve is not a gate waiver or permission to commit user work.

## Steps

1. **Run the plan's `Final Verify:` command**, not a task's nested check, and
   read the output. State honestly what it covered; record any unavailable or
   skipped checks as gaps rather than treating one green command as universal
   proof.
2. **Obtain at least one completed `to-skeptic` pass on the final candidate.**
   Hand it a complete evidence pack: the complete `plan.md` (including `## Notes`),
   each source alias and absolute cwd, frozen base and final candidate OIDs,
   exact diff and supporting files, literal Final Verify command, exit status
   and full output, prior findings/dispositions, and coverage gaps. Materialize
   worker-readable absolute paths or paste the full contents; a claim or the
   coordinator's earlier tool output is not a pack. Require confirmation that
   it read the pack. Skeptics are read-only, so dispatch
   several concurrently when the change warrants it, each with a distinct lens
   (does it do what it claims / what breaks it / does the evidence hold). One
   completed pass is the floor, not the ceiling.
    - Only a literal `Status: complete` is a completed pass. Run a blocked,
      missing, or unrecognized status's technical probes and resolve factual
      questions yourself; ask the user only for missing product intent. Then
      re-dispatch against the same candidate with the literal evidence.
      `Evidence requests: none` and `Questions: none` are compatible with
      `Status: complete`.
   - If accepted findings change code, the previous verdict describes an old
     tree. Re-run `Final Verify:`, then re-dispatch once against the new final
     candidate. Keep only the completed verdict for the tree being archived.
3. **Triage its findings.** Verify each finding against the candidate. A real
in-scope source defect becomes a new unchecked repair task with the complete
Owner/Repo/Cwd/Files/Change/Verify contract, appended to `plan.md` before code
changes. **Load `to-do` and continue in the same invocation**; its serial loop
re-dispatches `to-implementer` for the repair. Do not stop at a skeptic finding
or ask the user to continue when the repair is technically clear and in scope.
Decline only findings refuted by evidence, with a written reason. A code change
invalidates both the previous `Final Verify:` result and skeptic verdict; repeat
steps 1–2 on the new candidate before finishing.
   Before archival, identify each receiver needed for authorized integration.
   Prefer preallocating `homonto worktree receiver <name> --workflow to --repo <alias> --json`
   while active, before writing the final record below. Record the receiver
   identity, target and absolute path in that plan/handoff record. No receiver
   is needed when an authorized clean target checkout already exists. Do not
   make receiver allocation the first post-archive action.
4. **Record the outcome** under `## Verification` at the bottom of `plan.md`:
   the literal verify command and result, coverage gaps or skipped checks, and
   the skeptic's verdict (including declined findings). De-slop the prose, but
   record each verified source candidate OID and tree OID plus its intended
   integration target and canonical publication identity for recovery; to has
   no onto integration sidecar or receipt API. Do not replace those OIDs with
   the current tip on resume. Then
   in managed mode checkpoint it before archiving with
   `homonto workspace checkpoint --path tasks/<name>/plan.md --message "Record final verification"`.
   In existing mode leave this final record for the archive commit below.
5. **Assert and archive:**
   `to done <name> --verified --evidence "<the literal verify command and its result>" --dir "<configRoot>"`.
   The evidence string is recorded verbatim in the archived state — it is
   what makes this verification distinguishable from a skipped one later.
   The change moves to `<workflow-root>/tasks/archive/<date>-<name>/`.
6. **Record the archived result.** Managed `to done` automatically checkpoints
   the terminal state and directory move; do not manually stage managed records.
   Existing mode retains the manual commit of `## Verification`, terminal state,
   and archive move in the records' Git owner. Source integration remains in
   each source repo, never a merge of workflow history. Remove a registered
   worktree only after source integration and authorized terminal cleanup via
   `homonto worktree remove <name> --workflow to --repo <alias> --yes`.
7. **Integration verification before push.** Integrate only the recorded verified
   candidate when integration/publication is authorized; this step grants no new
   push permission. Follow the shared [publication contract](../homonto/references/publication.md).
   Use the recorded
   candidate, not a moving branch tip. Use the authorized clean target checkout,
   or validate and reuse the receiver path recorded before archive. For already
   archived recovery without a receiver, use the supported identity-checked
   terminal `homonto worktree receiver <name> --workflow to --repo <alias> --json`;
   do not demand impossible preallocation on resume or recreate state. An older
   active-only API with no safe receiver is a blocker. An occupied target requires exact-path
   inspection and an explicit release decision, never automatic checkout/cleanup.
   After joining isolated source commits,
   rerun `Final Verify:` in the actual receiving checkout and inspect the final
   tree before any push. If target changes or conflict resolution changes the
   reviewed tree, obtain a fresh completed skeptic pass with the full evidence
   pack for that integrated candidate. Pin its commit/tree and push only that
   reverified candidate. Do not treat pre-merge tests or `--verified` as proof
   of a new integration tree. Record recovery evidence outside immutable archives
   in the handoff/publication report; never invent an onto receipt for to.

On completed recovery, discover the archive by canonical PR identity and source
candidate/target evidence even when local PR HEAD is not ahead of upstream.
Completed source work may still be isolated and not locally integrated. Compare
its candidate with local receiving and remote heads, reconcile latest feedback,
and resume integration, verification, or delivery at the first missing step.
Only remaining authorized feedback needs a new continuation; do not duplicate
the archived work or edit the archive to absorb it.

## Rules

- **Never pass `--verified` before steps 1–4 are done.** The checkbox is
  self-asserted by design; asserting it without the work is lying in writing,
  in a reviewable artifact.
- If verification fails, stay in `do`, investigate the root cause, add or repair
  the task contract, fix it, and repeat the final pass. Stop only for a hard
  blocker or a scope decision that genuinely needs the user.
- Never hand-edit `to-state.yaml`; archiving is the binary's move, not `mv`.
