---
name: onto-close
description: onto phase 5 — close. Use when an active change has phase close (verification passed) — merges spec deltas, numbers and accepts ADR drafts, enforces the guides obligation, and archives the workspace after final confirmation.
---

# onto-close — Phase 5: Close

Land the change's knowledge where it lives permanently: living specs, the
ADR log, and user-facing guides — then archive the workspace.

## Entry check

- `onto-state.yaml` has `phase: close`; `verification.md` exists with a
  `Result: pass` line (a trailing `(N accepted deviations)` still counts —
  match the `Result: pass` prefix; the deviations are recorded inside the
  report).
- **Idempotent re-entry**: close mutates shared files (living specs, the
  ADR log). If `onto state <name> --json` shows `close.merged: true` (read it
  at entry), the deltas already landed on a prior, interrupted close — `onto
  merge-deltas` is a safe no-op in that case, but do NOT re-number ADRs by hand.
  Resume at the guides/gate/archive steps.
- Read `notes.md` at entry when present — the final gate is pre-authorized
  only by a directive that explicitly names closing/archiving (step 2).
- Anything else → route back through `/onto`.

## Steps

Close mutates shared, permanent files (living specs, the ADR log). So the
order is deliberate: **prepare and confirm first, mutate second, archive
atomically** — no global change happens before the final gate, and the
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
- Resolve the **guides obligation** (read via `onto state <name> --json`);
  archiving with `guides: pending` is prohibited. Either write/update the
  affected `docs/guides/<topic>.md` (and README if user-visible) then `onto
  set guides <name> updated`, **or** record `onto set guides <name> "waived:
  <reason>"` (the reason comes from the user or a recorded directive, never
  invented). Guide prose gets the onto-no-slop
  pass; the specs and ADRs do not yet exist in living form, so they wait
  for step 3.
- Assemble the **close plan**: each workspace delta → its target
  `docs/specs/<capability>.md` and the operations it applies; each ADR
  draft → its next number and slug; the guides outcome; the deferred tasks
  executed. This plan is what the gate shows.

### 2. Final confirmation gate (before any spec or ADR mutation)

> **GATE (final confirmation):** present the close plan — deltas to merge
> and how, ADR numbers + slugs to assign, guides outcome, deferred tasks
> done — and ask for confirmation to merge and archive. Nothing global has
> changed yet, so a declined gate leaves the repo untouched.
>
> This gate MAY be pre-authorized **only** by a recorded directive that
> explicitly authorizes closing/archiving this change (e.g. "close and
> archive it when done"). A generic build directive like "run to
> completion with defaults" does **not** reach this gate — archiving is
> irreversible; a directive must name it. Still surface the plan.
>
> When confirmed, record the evidence token — `onto merge-deltas` and
> `onto close` both refuse without it, so nothing global can mutate ahead
> of the user's yes:
>
> ```
> onto set close-confirmed <name> "YYYY-MM-DD <confirmation summary>"
> ```

### 3. Execute the close (only after confirmation)

1. **Merge spec deltas — via the binary.** Run **`onto merge-deltas <name>`**.
   It deterministically merges every workspace delta `specs/<capability>.md`
   into `docs/specs/<capability>.md`, applying sections **RENAMED → MODIFIED →
   REMOVED → ADDED** in that fixed order (so a MODIFIED targeting a just-renamed
   name resolves), lints the result (no leaked delta headings, no duplicated
   requirement), writes nothing unless **every** delta merges and lints clean
   (transactional), and sets `close.merged`. It is idempotent — a change already
   `close.merged` is a no-op, so an interrupted close re-runs safely. A capability
   with no living spec is created with a plain `## Requirements` heading. If it
   errors (a MODIFIED/REMOVED/RENAMED-FROM name absent, or an ADDED name that
   already exists), fix the delta and re-run — do not hand-edit the living spec.

   The merged spec reads as "always true, now" — no change-log language. **The
   binary does not rewrite normative prose**: it moves requirement blocks
   verbatim, so `SHALL`/`MUST` lines, scenarios, and machine-read markers are
   untouched. Run onto-no-slop only over *genuinely new* guide/ADR prose, never a
   merged requirement's wording.
3. **Number and accept ADRs.** For each draft in the workspace `adr/`:
   next free number = highest `NNNN` in `docs/adr/` + 1; `git mv` to
   `docs/adr/NNNN-<slug>.md`; set `Status: Accepted` (and any superseded
   ADR → `Superseded by NNNN`). Assign numbers to all drafts in one pass
   before moving any, so two drafts in this change never collide.
   **Guard against a concurrent close** (the framework runs one worktree
   per active change, so two may close near the same time): re-scan
   `docs/adr/` for the highest number **immediately before each `git mv`**,
   not once up front — if a number you planned now exists on disk, another
   change took it; recompute from the current highest and continue. Never
   overwrite an existing `docs/adr/NNNN-*.md`. If a move still collides,
   stop and resolve by hand — a clobbered ADR is unrecoverable.
   Then rewrite the workspace's `design.md` and `notes.md` references from
   `adr/<slug>.md` to the final `docs/adr/NNNN-<slug>.md` path — otherwise
   the archive ships dangling ADR references.
4. Run lint-checklist section 3 (post-merge: no delta-only headings leaked,
   no duplicated requirements, scenario structure intact) and section 4
   (guides resolved, no dangling references). Findings block the archive.
5. **Commit the close preparation.** Steps 1, 3, and the step-1 guides
   resolution dirtied shared files (`docs/specs/`, `docs/adr/`,
   `docs/guides/`) plus the workspace's own `onto-state.yaml` (merge-deltas
   set `close.merged`) and its `design.md`/`notes.md` references. `onto close`
   refuses to archive a dirty worktree, so this preparation MUST be committed
   before invoking it:

   ```
   git add -A && git commit -m "close <name>: merge specs, accept ADRs, resolve guides"
   ```

   This commit is the "prepare" half of the close; the archive move below is
   the second commit. The advertised "one archive commit" covers the workspace
   move only — the shared-spec/ADR/guide landings are a separate, named commit
   because they describe global mutations, not the workspace's archival.
6. **Archive via the binary**: `onto close <name>` — it verifies the change is
   at `close`, all `deps` are archived, and the worktree is clean (other
   active changes' uncommitted `docs/changes/<other>/` files are tolerated —
   they gate their own close; if it refuses, `onto dirt <name>` lists what
   blocks and the dispatcher's `dirty-workspace.md` says how to attribute
   it — never launder unrelated dirt into the archive commit), then moves
   `docs/changes/<name>` to `docs/changes/archive/YYYY-MM-DD-<name>` and sets
   `archived: true` in one operation. Commit the move (`git add -A && git
   commit`). `phase` stays `close`; "done" is derived-only, never written. The
   archived workspace is history — never edited after, with one sanctioned
   exception: `ship.md`.

### 4. Integrate the branch (merge or PR)

Integrate the change's git branch into the project, per the recorded
`integration` choice (`onto state <name> --json` → `integration`). If it is
unset, **ask via a dialog** which the user wants and record it with `onto set
integration <name> merge|pr` before acting:

- **`merge`** — merge the change branch into its base ref. Determine the base
  (`base_ref` in state, else the repo's default branch) and the change branch
  (the current branch, or the isolation worktree's branch). Run the merge
  (`git checkout <base>` → `git merge --no-ff <change-branch>`); on a conflict,
  **stop**, `git merge --abort`, and hand the conflict to the user — never
  force-resolve. On success, report the merge.
- **`pr`** — push the branch (`git push -u origin <change-branch>`) and open a
  pull request with `gh pr create --base <base> --fill` (title/body from the
  archived change; reuse `references/ship-handoff.md` for the body). Report the
  PR URL. The branch stays open for review — it is merged on the platform, not
  locally. If `gh` or a remote is unavailable, WARN and fall back to writing the
  ready PR body to the archive's `ship.md` for the user to open manually.

Do this **after** the archive commit (step 3.6), so the integrated branch
includes the archived workspace. `close.merged` tracks spec-delta merging and is
unrelated to this git integration — both happen at close.

## Exit checklist

- [ ] Final confirmation given **before** any spec/ADR mutation (or a
      directive that explicitly authorized closing/archiving), recorded via
      `onto set close-confirmed <name> "<evidence>"` — merge-deltas and
      close refuse without it
- [ ] `onto merge-deltas <name>` run — living specs merged deterministically and
      lint-clean, `close.merged` set (idempotent; transactional)
- [ ] Lint checklist fully passed (pre-merge §1–2, post-merge §3 incl. the
      duplicate-requirement check, pre-archive §4 dangling refs)
- [ ] Every delta spec merged (RENAMED→MODIFIED→REMOVED→ADDED); living
      specs read as current truth with no duplicated requirements
- [ ] Every ADR draft numbered, accepted, moved to `docs/adr/`; workspace
      references rewritten to the final paths
- [ ] `onto set guides <name> updated` or `… "waived: <reason>"` — never pending
- [ ] onto-no-slop pass run over **new** guide/ADR prose only, recorded in
      `notes.md` (`no-slop: <artifact> done`); no requirement wording, `SHALL`/`MUST` line, scenario, or
      machine-read marker was rewritten
- [ ] Close preparation committed (specs, ADRs, guides, workspace references,
      `onto-state.yaml`) — the worktree is clean before `onto close`
- [ ] Archive is its own commit: workspace under
      `docs/changes/archive/YYYY-MM-DD-<name>/` **and** `archived: true`,
      committed together, everything tracked
- [ ] Branch integrated per the `integration` choice — merged into base (clean,
      no forced conflict resolution) or a PR opened (URL reported); `ship.md`
      fallback written only if `gh`/remote was unavailable
- [ ] Announce completion and summarize where the knowledge landed
