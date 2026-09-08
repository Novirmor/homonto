---
name: onto-close
description: onto phase 5 — close. Use when an active change has phase close (verification passed) — validates the close plan, merges spec deltas, numbers and accepts ADR drafts, resolves guides, archives the workspace, and integrates the branch.
---

# onto-close — Phase 5: Close

Land the change's knowledge where it lives permanently: living specs, the
ADR log, and user-facing guides — then archive the workspace.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry.
All workflow calls keep `--dir "<configRoot>"`; source commands use selected
execution roots. Managed records history is never source integration.

## Entry check

- `onto-state.yaml` has `phase: close`; `verification.md` exists with a
  `Result: pass` line (a trailing `(N accepted deviations)` still counts —
  only the two canonical forms count, and exactly one `Result:` marker may
  appear; the deviations are recorded inside the report).
- **Idempotent re-entry**: close mutates shared files (living specs, the
  ADR log). If `onto state <name> --json` shows `close.merged: true` (read it
  at entry), the deltas already landed on a prior, interrupted close — `onto
  merge-deltas` is a safe no-op only when its versioned receipt matches the
  exact delta manifest and living-spec post-images. A mismatch fails closed; it
   never clears the marker or replays over newer content. Do not re-number
   already-promoted ADRs. This receipt proves spec merging only: reconcile ADR promotion
   independently, preserving completed moves and assigned numbers, then resume
   at the first incomplete guides/validation/archive step.
- Read `notes.md` at entry when present and honor any explicit endpoint or
  integration constraint already recorded.
- Anything else → route back through `/onto`.

## Steps

Close mutates shared, durable files (living specs, the ADR log). So the
order is deliberate: **prepare and validate first, mutate second, archive
atomically** — no global change happens before the close plan is checked, and the
one interruption-prone step (mv + archived flag) is a single commit.

### 1. Lint and prepare (blocking, no global mutation yet)

- Run `references/lint-checklist.md` sections 0–2 (delta coverage, delta
  format, workspace state). Section 0 is the one that catches a behavior
  change shipping with no spec. Findings block close — fix or stop. This
  replaces the format validation the retired external tooling performed.
- Execute any `DEFERRED to close:` tasks from `tasks.md` now (they must be
  non-runtime — bookkeeping, file moves, doc stamps — because verify never
  exercised them). Rewrite each executed line to
  `- [x] N.N (deferred, done at close YYYY-MM-DD): <desc> [trace #K]` and note the
  evidence. If executing one turns out to change runtime behavior, **stop**:
  it should have been built before verify. Route back to build, add a task,
  re-verify — closing unverified runtime behavior is exactly the hole the
  deferral rule exists to prevent.
- Resolve the **guides obligation** for a full workflow (read via `onto state
  <name> --json`); archiving it with `guides: pending` is prohibited. Presets
  leave guides unset, but must resolve a carried pending value. Write or update
  the affected `<workflow-root>/guides/<topic>.md` (and README if user-visible), then run
  `onto set guides <name> updated`. Ask only when a guide update is genuinely
  unwanted and a waiver is needed; record `onto set guides <name> "waived:
  <reason>"` with the user's or a recorded directive's reason, never an
  invented one. Guide prose gets the onto-no-slop pass; the specs and ADRs do
  not yet exist in living form, so they wait for step 3.
- Resolve **integration** before assembling the close plan. Honor a recorded
  choice; otherwise use `pr` when repository policy requires remote review and
  default to local `merge`. Run `onto set integration <name> merge|pr` now,
  while the workspace is active. Ask only if repository policy is contradictory
  or the choice changes an external commitment.
- Resolve **the integration branch** separately from `base_ref`. In schema 2,
  inspect each alias's `repo_bases` and validate with
  `onto set base-branch <name> <branch> --repo <alias> --dir "<configRoot>"`;
  it cannot retarget an immutable anchor. Honor legacy scalar `base_branch`;
  if missing, derive the intended source target, then use the scalar setter.
  Each `base_ref` remains the
  immutable commit used for diff and verification and is never a checkout or PR
  base.
- Assemble the **close plan**: each workspace delta → its target
  `<workflow-root>/specs/<capability>.md` and the operations it applies; each ADR
  draft → its next number and slug; the guides outcome; the deferred tasks
  executed. This plan is what the gate shows.
- Identify receivers now. When integration needs a new receiving checkout,
  prefer `homonto worktree receiver <name> --workflow onto --repo <alias> --json`
  before archive, recording its identity and absolute path. Do not wait until
  the active-only allocation window has closed. Already archived recovery uses
  a validated existing receiver or the supported identity-checked terminal API,
  never recreates active state to satisfy this preference.

### 2. Validate the close plan (before any spec or ADR mutation)

Check every close-plan entry against the verified workspace and target files.
Repair mapping, numbering, guide, or deferred-task mistakes before mutation.
Present a concise plan for visibility, then record the evidence token without a
second approval round:

```
onto set close-confirmed <name> "YYYY-MM-DD <validated close-plan summary>"
```

`onto merge-deltas` and `onto close` both refuse without this token. It records
that the close review happened; it does not claim the user personally reviewed
the plan.

### 3. Execute the close (only after validation)

1. **Merge spec deltas — via the binary.** Run **`onto merge-deltas <name>`**.
   It deterministically merges every workspace delta `specs/<capability>.md`
   into `<workflow-root>/specs/<capability>.md`, applying sections **RENAMED → MODIFIED →
   REMOVED → ADDED** in that fixed order (so a MODIFIED targeting a just-renamed
   name resolves), lints the result (no leaked delta headings, no duplicated
   requirement), writes nothing unless **every** delta merges and lints clean
   (transactional), records exact pre/post-image hashes in
   `.onto/merge-receipt.json`, and sets `close.merged`. It is idempotent when the
   receipt still matches, and resumes an interrupted multi-file write only from
   recorded pre/post-images. Changed deltas or targets fail closed. A capability
   with no living spec is created with a plain `## Requirements` heading. If it
   errors (a MODIFIED/REMOVED/RENAMED-FROM name absent, or an ADDED name that
   already exists), fix the delta and re-run — do not hand-edit the living spec.

   The merged spec reads as "always true, now" — no change-log language. **The
   binary does not rewrite normative prose**: it moves requirement blocks
   verbatim, so `SHALL`/`MUST` lines, scenarios, and machine-read markers are
   untouched. Run onto-no-slop only over *genuinely new* guide/ADR prose, never a
   merged requirement's wording.
2. **Number and accept ADRs.** For each draft in the workspace `adr/`:
   next free number = highest `NNNN` in `<workflow-root>/adr/` + 1; move to
   `<workflow-root>/adr/NNNN-<slug>.md`; set `Status: Accepted` (and any superseded
   ADR → `Superseded by NNNN`). Assign numbers to all drafts in one pass
   before moving any, so two drafts in this change never collide.
   **Guard against a concurrent close** (the framework runs one worktree
   per active change, so two may close near the same time): re-scan
   `<workflow-root>/adr/` for the highest number **immediately before each move**,
   not once up front — if a number you planned now exists on disk, another
   change took it; recompute from the current highest and continue. Never
   overwrite an existing `<workflow-root>/adr/NNNN-*.md`. If a move still collides,
   re-scan and retry with the next free number; report a hard blocker only when
   the filesystem keeps changing and safe numbering cannot converge.
   Then rewrite the workspace's `design.md` and `notes.md` references from
   `adr/<slug>.md` to the final `<workflow-root>/adr/NNNN-<slug>.md` path — otherwise
   the archive ships dangling ADR references. In managed mode use a filesystem
   move and checkpoint the named old/new record paths; do not use `git mv`, which
   stages the managed index. Existing mode retains `git mv` in the records' owner.
3. Run lint-checklist section 3 (post-merge: no delta-only headings leaked,
   no duplicated requirements, scenario structure intact) and section 4
   (guides resolved, no dangling references). Findings block the archive.
4. **Record the close preparation.** Steps 1, 2, and the step-1 guides
   resolution dirtied shared files (`<workflow-root>/specs/`, `<workflow-root>/adr/`,
   `<workflow-root>/guides/`) plus the workspace's own `onto-state.yaml` (merge-deltas
   set `close.merged`) and its `design.md`/`notes.md` references. `onto close`
   refuses blocking dirt, so record preparation before invoking it. In managed
   mode, checkpoint manual Markdown before each following binary mutation:

   ```sh
   homonto workspace checkpoint --path changes/<name> --path <named-spec-path> --path <named-old-ADR-path> --path <named-new-ADR-path> --path <named-guide-path> --message "Record close preparation"
   ```

   Paths are workflow-relative; include only touched, owned records. Binary
   merge/state writes checkpoint automatically. Inspect pending history and use
   `homonto workspace recover` before further mutations if needed. In existing
   mode retain the manual preparation commit in the records' Git owner:

   ```
   git add -- <named touched specs, ADRs, guides, and <workflow-root>/changes/<name> paths>
   git diff --cached --name-only
   git commit -m "close <name>: merge specs, accept ADRs, resolve guides"
   ```

   In existing mode this commit is the "prepare" half of close; the archive move below is
   the second commit. The advertised "one archive commit" covers the workspace
   move only — the shared-spec/ADR/guide landings are a separate, named commit
   because they describe global mutations, not the workspace's archival.
5. **Archive via the binary**: `onto close <name>` — it verifies the change is
    at `close`, every cumulative artifact exists, the report contains exactly
    one canonical passing result, the merge receipt matches, both `base_ref` and
    `base_branch` are recorded, all `deps` are complete, and the worktree is clean (other
   active changes' uncommitted `<workflow-root>/changes/<other>/` files are tolerated —
   they gate their own close; if it refuses, `onto dirt <name>` lists what
   blocks and the dispatcher's `dirty-workspace.md` says how to attribute
    it — never launder unrelated dirt into the archive commit), then moves
     `<workflow-root>/changes/<name>` to `<workflow-root>/changes/archive/YYYY-MM-DD-<name>` and sets
     `archived: true` with a pending `.onto/integration.json`. If interruption
     lands the directory in `archive/` with `archived: false`, rerun `onto close
     <name>`; the binary completes the interrupted move. Stage only the old and new workspace
    paths, inspect the staged names, and commit the move in existing mode only.
    Managed `onto close` checkpoints the move and state automatically; never
    manually stage its records. `phase` stays `close`;
    "done" is derived-only, never written. The
    archived workspace is history — never edited after, with two sanctioned
    exceptions: `ship.md` and the one-way integration receipt.

### 4. Integrate the branch (merge or PR)

Follow [verified source publication](../homonto/references/publication.md)
for exact candidate pinning, canonical PR identity, and origin-only closing markers.

Read the recorded source commit and target branch from the archived
`.onto/integration.json` for each selected source alias, then integrate per the
recorded choice. Schema 2 has no implicit config entry. With separate or managed
records, use the exact recorded verified candidate as `<sourceCommit>`; never use a
records archive/checkpoint SHA as source. Only an existing combined checkout has
an `<archiveCommit>` containing both source and archive bookkeeping, and it is
eligible only after proving the intervening diff is owned records-only. Never
take the source branch's current tip beyond the recorded candidate.

Route per-repo no-op before merge/PR: when the source appears unchanged, run
`onto complete-integration <name> --receipt "unchanged:<receivingSHA>" --repo <alias> --dir "<configRoot>"`.
The binary proves it against the recorded source/target. On success that repo is
complete in either integration mode; do not manufacture an empty PR or merge.
On refusal investigate and use the actual delivery route when source changed.
Do not pass `--head` for unchanged or merge. Remaining repos proceed independently.

- **`merge`** — after recording the archive, pin the source integration candidate
  as described above. Do not use a moving branch name or the commit-valued
  diff base. Determine the source branch from its selected execution binding.
  With branch isolation, check out that repo's recorded `base_branch` and run
  `git merge --no-ff <sourceCommit>` (use `<archiveCommit>` only in existing
  combined mode). With worktree isolation, locate the
  existing clean worktree that has `base_branch` checked out and run the
  merge there; Git will not check out one branch in two worktrees. Validate and
  reuse the preallocated receiver's recorded path after archive. If an older
  completed run has no safe receiving checkout, use the supported identity-checked
  terminal `homonto worktree receiver <name> --workflow onto --repo <alias> --json`
  per the shared policy; an occupied target, unsupported terminal API, or denial is
  not permission to switch a dirty original or allocate a raw worktree. Resolve
  mechanical conflicts from the verified change and repository history, then
  re-run relevant checks. If a conflict requires choosing product behavior,
  abort and ask; never guess or discard either side. Inspect the target before
  writes: if preserved dirt blocks this merge, do not clean it automatically or
  treat isolation as permission to touch it. On success, report the merge.
- **`pr`** — assemble a neutral shared base per `references/ship-handoff.md`,
  with the canonical original issue link but no closing directive. Write only
  that neutral base to archived `ship.md` and record the sanctioned addition with
  `homonto workspace checkpoint --path changes/archive/YYYY-MM-DD-<name>/ship.md --message "Record PR handoff"`
  in managed mode, or a named manual records commit in existing mode;
  if a committed `ship.md` already exists, leave it immutable. For every repo
  and retry regenerate a separate session-tmp body; strip any old cached closing
  directive from the temporary rendering and add `Closes #N` only after confirming
  the destination is the original issue's canonical origin. Never reuse an
  origin-specific body for another repo. Validate destination ID/host, target,
  observed headOID, body path and hash before publication/receipt, per the shared
  contract. No new `ship/` archive subtree is authorized.
  Pin the verified delivery OID and push only that candidate
  with `git push "$REMOTE" "$DELIVERY_OID:refs/heads/$CHANGE_BRANCH"`, then look for an existing PR before creating one —
  reading the ref names into shell parameters and passing them quoted, since
  Git refs can carry `$()` and quotes. Push from the source root, and supply an
  absolute per-repo temporary body path for publication:
  `gh pr list --repo HOST/OWNER/REPO --head "$CHANGE_BRANCH" --base
  "$BASE_BRANCH" --state open --json number,url`. Paginate and fetch each
  candidate's canonical head repository ID/host, owner/ref, delivered headRefOid,
  and base repository/target; branch-name-only filtering is insufficient.
  A single exact OPEN match after delivery is the receipt, never an unrelated
  same-name fork branch. No match after complete enumeration means create with
  `gh pr create --repo HOST/OWNER/REPO --head "$QUALIFIED_HEAD" --base
  "$BASE_BRANCH" --fill --body-file "$REPO_BODY_FILE"` (the explicit
  `--head` matters: `gh pr create` otherwise targets the current branch,
  which may be the base).
  Several matches → stop and ask; an unrelated PR must never pass as this
  change's receipt. Report the PR URL. The branch stays open for review — it
  is merged on the platform, not locally. If `gh` or a remote is
  unavailable, WARN and report the destination-specific temporary body path
  and pending integration. Neutral `ship.md` is not itself ready to post.

After a local merge succeeds, run `onto complete-integration <name> --receipt
"merge:<merge-commit>"` — the binary verifies the receipt against real history
(it must be a `--no-ff` merge containing the recorded source commit, reachable
from the recorded base branch) and canonicalizes it to the full commit id.
After a PR opens or is reused, independently verify its canonical remote head
and target, then run `onto complete-integration <name> --receipt "pr:<https-url>" --head <observed-headOID> --repo <alias> --dir "<configRoot>"`.
The head is the observed full remote OID, not guessed local HEAD. Onto records
an external claim and does not verify remote publication. In schema 2 use
`--repo <alias> --dir "<configRoot>"` for each
selected source repo's own receipt, including merge receipts above. There is no
implicit config receipt. Legacy combined mode retains the no-`--repo` config
receipt plus each selected sibling's receipt. The change derives `done` only
when every recorded repository is complete. Managed receipt writes checkpoint
automatically; existing mode commits the sidecar in the records' Git owner and
pushes it when applicable. The command is one-way and idempotent for the same
receipt. Temporary-body/neutral-`ship.md` fallback remains pending because no PR
exists yet, unless that repo was already proven unchanged.

Do this **after** recording the archive (step 3.5). Only existing combined mode
integrates archive bookkeeping with source; never merge managed workflow history
into a source repo. For registered bindings, with removal authorized after integration, run
`homonto worktree remove <name> --workflow onto --repo <alias> --yes` from
configRoot. A PR merely opened may not be integrated yet, so removal can refuse;
keep its binding and report, never force cleanup. Legacy schema 0/1 combined raw
worktrees use the exact-path, clean-and-integrated teardown in
`onto-build/references/worktree-protocol.md`, not the registered remove command.
`close.merged` tracks spec-delta merging and is
unrelated to ADR promotion or Git integration — all are separate close steps.

One boundary the binary enforces for you: source commits that land after the
recorded verification pass refuse `onto close` ("re-verify the change"). If
close reports that, do not bypass it — re-run the verification at the new HEAD
and record the fresh pass, or follow the reopen path if the change needs a
real fix.

## Exit checklist

- [ ] Close plan validated **before** any spec/ADR mutation and recorded via
      `onto set close-confirmed <name> "<evidence>"` — merge-deltas and close
      refuse without it
- [ ] `onto merge-deltas <name>` run — living specs merged deterministically and
      lint-clean, `close.merged` set (idempotent; transactional)
- [ ] Lint checklist fully passed (pre-merge §1–2, post-merge §3 incl. the
      duplicate-requirement check, pre-archive §4 dangling refs)
- [ ] Every delta spec merged (RENAMED→MODIFIED→REMOVED→ADDED); living
      specs read as current truth with no duplicated requirements
- [ ] Every ADR draft numbered, accepted, moved to `<workflow-root>/adr/`; workspace
      references rewritten to the final paths
- [ ] `onto set guides <name> updated` or `… "waived: <reason>"` — never pending
- [ ] `onto set integration <name> merge|pr` recorded before archive
- [ ] `base_branch` recorded and distinct from the commit-valued `base_ref`
- [ ] onto-no-slop pass run over **new** guide/ADR prose only, recorded in
      `notes.md` (`no-slop: <artifact> done`); no requirement wording, `SHALL`/`MUST` line, scenario, or
      machine-read marker was rewritten
- [ ] Close preparation recorded through managed checkpoints or existing-mode
      named commits; selected execution roots and records meet their clean gates
- [ ] Archive is its own binary checkpoint (managed) or manual commit (existing): workspace under
       `<workflow-root>/changes/archive/YYYY-MM-DD-<name>/` **and** `archived: true`,
      committed together, everything tracked
- [ ] Every source has a proven `unchanged:` receipt, a real merge receipt, or
      an opened/reused PR receipt with observed `--head`; no empty PR/merge was
      manufactured. Any unpublishable repo has a destination-specific temporary
      body and an explicit pending-integration report, not a claim of done.
- [ ] `onto complete-integration <name> --repo <alias> --receipt <receipt> --dir "<configRoot>"`
      recorded for every selected source alias in schema 2, with no implicit
      config receipt; legacy mode retains its config-plus-siblings receipts.
       Managed writes checkpoint automatically; existing writes are committed.
       For a PR receipt include `--head <observed-headOID>`; never for merge/unchanged.
      `onto state <name> --json --dir "<configRoot>"` derives `done`
- [ ] Announce completion and summarize where the knowledge landed
