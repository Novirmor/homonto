---
name: onto-close
description: onto phase 5 — close. Use when an active change has phase close (verification passed) — validates the close plan, merges spec deltas, numbers and accepts ADR drafts, resolves guides, archives the workspace, and integrates the branch.
---

# onto-close — Phase 5: Close

Land the change's knowledge where it lives permanently: living specs, the
ADR log, and user-facing guides — then archive the workspace.
Apply the dispatcher's shared autonomous workflow policy throughout.

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
  never clears the marker or replays over newer content. Do not re-number ADRs
  by hand.
  Resume at the guides/validation/archive steps.
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
  `- [x] N.N (deferred, done at close YYYY-MM-DD): <desc>` and note the
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
- Resolve **the integration branch** separately from `base_ref`. Honor recorded
  `base_branch`; for a legacy state derive the repository's intended target
  branch, then run `onto set base-branch <name> <branch>`. `base_ref` remains the
  immutable commit used for diff and verification and is never a checkout or PR
  base.
- Assemble the **close plan**: each workspace delta → its target
  `<workflow-root>/specs/<capability>.md` and the operations it applies; each ADR
  draft → its next number and slug; the guides outcome; the deferred tasks
  executed. This plan is what the gate shows.

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
   next free number = highest `NNNN` in `<workflow-root>/adr/` + 1; `git mv` to
   `<workflow-root>/adr/NNNN-<slug>.md`; set `Status: Accepted` (and any superseded
   ADR → `Superseded by NNNN`). Assign numbers to all drafts in one pass
   before moving any, so two drafts in this change never collide.
   **Guard against a concurrent close** (the framework runs one worktree
   per active change, so two may close near the same time): re-scan
   `<workflow-root>/adr/` for the highest number **immediately before each `git mv`**,
   not once up front — if a number you planned now exists on disk, another
   change took it; recompute from the current highest and continue. Never
   overwrite an existing `<workflow-root>/adr/NNNN-*.md`. If a move still collides,
   re-scan and retry with the next free number; report a hard blocker only when
   the filesystem keeps changing and safe numbering cannot converge.
   Then rewrite the workspace's `design.md` and `notes.md` references from
   `adr/<slug>.md` to the final `<workflow-root>/adr/NNNN-<slug>.md` path — otherwise
   the archive ships dangling ADR references.
3. Run lint-checklist section 3 (post-merge: no delta-only headings leaked,
   no duplicated requirements, scenario structure intact) and section 4
   (guides resolved, no dangling references). Findings block the archive.
4. **Commit the close preparation.** Steps 1, 2, and the step-1 guides
   resolution dirtied shared files (`<workflow-root>/specs/`, `<workflow-root>/adr/`,
   `<workflow-root>/guides/`) plus the workspace's own `onto-state.yaml` (merge-deltas
   set `close.merged`) and its `design.md`/`notes.md` references. `onto close`
   refuses to archive a dirty worktree, so this preparation MUST be committed
   before invoking it:

   ```
   git add -- <named touched specs, ADRs, guides, and <workflow-root>/changes/<name> paths>
   git diff --cached --name-only
   git commit -m "close <name>: merge specs, accept ADRs, resolve guides"
   ```

   This commit is the "prepare" half of the close; the archive move below is
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
    paths, inspect the staged names, and commit the move. `phase` stays `close`;
    "done" is derived-only, never written. The
    archived workspace is history — never edited after, with two sanctioned
    exceptions: `ship.md` and the one-way integration receipt.

### 4. Integrate the branch (merge or PR)

Read the source and target branches from the archived
`.onto/integration.json`, then integrate per the recorded choice:

- **`merge`** — merge the change branch into `base_branch`, never the
  commit-valued `base_ref`. Determine the change branch from the current branch
  or isolation worktree. With branch isolation, check out `base_branch` and run
  `git merge --no-ff <change-branch>`. With worktree isolation, locate the
  existing clean worktree that has `base_branch` checked out and run the merge
  there; Git will not check out one branch in two worktrees. Resolve
  mechanical conflicts from the verified change and repository history, then
  re-run relevant checks. If a conflict requires choosing product behavior,
  abort and ask; never guess or discard either side. On success, report the merge.
- **`pr`** — push the branch (`git push -u origin <change-branch>`) and open a
  pull request with `gh pr create --base <base_branch> --fill` (title/body from the
  archived change; reuse `references/ship-handoff.md` for the body). Report the
  PR URL. The branch stays open for review — it is merged on the platform, not
  locally. If `gh` or a remote is unavailable, WARN and fall back to writing the
  ready PR body to the archive's `ship.md` for the user to open manually.

After a local merge succeeds, run `onto complete-integration <name> --receipt
"merge:<merge-commit>"` — the binary verifies the receipt against real history
(it must be a `--no-ff` merge containing the recorded source commit, reachable
from the recorded base branch) and canonicalizes it to the full commit id.
After a PR opens, run `onto complete-integration <name> --receipt
"pr:<https-url>"`. A change with selected `--repo` siblings completes one
repository at a time: run the command once without `--repo` for the config
repository, then once per sibling with `--repo <alias>` and that repository's
own receipt; the change derives `done` only when every repository is complete.
Commit each sidecar update on the branch that now contains the archive and
push it when applicable. The command is one-way and idempotent for the same
receipt. A `ship.md` fallback remains pending because no PR exists yet.

Do this **after** the archive commit (step 3.5), so the integrated branch
includes the archived workspace. `close.merged` tracks spec-delta merging and is
unrelated to this git integration — both happen at close.

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
- [ ] Close preparation committed (specs, ADRs, guides, workspace references,
      `onto-state.yaml`) — the worktree is clean before `onto close`
- [ ] Archive is its own commit: workspace under
       `<workflow-root>/changes/archive/YYYY-MM-DD-<name>/` **and** `archived: true`,
      committed together, everything tracked
- [ ] Branch integrated per the `integration` choice — merged into base (clean,
      no forced conflict resolution) or a PR opened (URL reported); `ship.md`
      fallback written only if `gh`/remote was unavailable
- [ ] `onto complete-integration <name> [--repo <alias>] --receipt <receipt>`
      recorded and committed for the config repository **and every selected
      sibling**; `onto state <name> --json` derives `done`
- [ ] Announce completion and summarize where the knowledge landed
