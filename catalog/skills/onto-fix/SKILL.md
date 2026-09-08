---
name: onto-fix
description: onto preset — bug fix. Use for behavior fixes that need no new capability design — open-lite, then build starting from a failing test that reproduces the bug, verify, close; upgrades to the full workflow when scope grows.
---

# onto-fix — Preset: Bug Fix

Fast path for fixing broken behavior: **open-lite → build → verify → close**.
Skips the design phase — which is exactly why the upgrade rules below are
non-negotiable.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry. Continue
through the entire preset unless the user names an endpoint or asks to pause.

## Entry check

- A new bug-fix request (clear broken behavior), or an active change with
  `workflow: fix`. This preset owns the change's whole lifecycle; the
  dispatcher routes every phase of a fix change here.
- Not for new capabilities, refactors, or behavior *changes* — those are
  full-workflow work via `onto-open`.
- Read `notes.md` at entry when present. If any skill's `references/`
  directory is missing, degrade per the dispatcher rule: note the gap and
  fall back to the SKILL.md tables, continue.
- **Resume map** (the dispatcher routes every phase of a fix change here;
  a fresh session must not re-run completed steps). Read recorded phase and setup
  before using derived phase:

  | Recorded/setup or derived phase | Enter at |
  |---|---|
  | recorded open/design, or missing proposal review/isolation/task contracts | step 1 setup resume; preserve state, fill only missing artifacts/decisions, never rerun `new` |
  | build with setup complete | step 2, first unchecked task; inspect and reconcile partial work, never redo a committed task |
  | verify | step 3 |
  | close | step 4 |

  Only a brand-new request runs `onto new`. An existing empty preset scaffold
  may derive build while recorded open; it still needs setup and gated advancement.
  Setup repair at recorded build/verify/close fills only missing artifacts or
  decisions and preserves the recorded phase. Run `advance --to build` only
  when recorded phase is open or design, never after it already reached build.
  Report-only repair refreshes report/evidence as needed, without fake phase hops.

## Steps

### 1. Open-lite

Minimal clarification: reproduction steps, expected vs actual behavior,
suspected blast radius. Create `<workflow-root>/changes/<name>/` with:

Before any scaffold, initialize the authorized empty managed records root.
Choose isolation before `new`; allocate schema-2 bindings immediately after
creation, before any records/source commit. Resume validated isolation instead
of allocating again; a frozen-base mismatch blocks, never retarget it.

- Create the workspace via `onto new <name> --workflow fix`, adding one `--repo
  <alias>` for every selected source in schema 2, with `--dir "<configRoot>"`.
  Add repeated `--base <alias>=<local-branch>` at creation for alternative bases,
  such as `--base api=main --base web=develop`; otherwise each source's current
  committed HEAD and local branch are frozen. To isolate from `main` while on
  a dirty feature branch, select `api=main` here without switching or cleaning
  the original. (`onto new`
  creates `onto-state.yaml` carrying `workflow: fix`, `phase: open`,
  `created`, and empty `proposal.md`/`tasks.md`). Then:
  - Inspect schema 2 `repo_bases` captured at creation; validate each with
    `onto set base-ref <name> <commit> --repo <alias> --dir "<configRoot>"` and
    `onto set base-branch <name> <branch> --repo <alias> --dir "<configRoot>"`.
    These anchors are immutable; setters do not retarget them after creation.
    Worktree creation must match the frozen commit and target. Never use records
    HEAD as source. Legacy combined mode retains scalar setters.
  - `onto set deps <name> --dep <a> --dep <b>` for prerequisite active changes
    identified from the request or repository (omit when there are none)
  - default the decisions (presets enter build directly): `onto set isolation
    <name> branch|worktree`, `onto set build-mode <name> direct`, **`onto set tdd-mode
    <name> tdd`** — a fix's whole method is a failing test that reproduces the
    bug first, so its build runs the TDD branch; never default a fix to
    `tdd-mode direct`.
- `proposal.md` uses [the reduced preset template](../onto-open/references/preset-proposal.md):
  `Preset: fix`, Why/reproduction, What Changes, Non-Goals, Capability Impact,
  Acceptance Scenarios, and non-empty Grounding. Close lint uses that template.
- `tasks.md` — short checklist (reproduce → fix → regression). The
  checklist is live during the fix: in-scope discovered work is APPENDED
  as a new unchecked item before its code is written, checked off as its
  commit lands — never done silently (scope-exceeding work hits the
  upgrade gate instead)

No full design and no plan.md required. Inspect dirt and resolve the shared
preserve/isolate/cleanup choice once before writes. Use registered `worktree`
bindings when isolation is chosen and `branch` for a safe serial change. Create the selected
isolation and use branch `fix/YYYYMMDD/<name>` before implementation.
Templates: use the reduced preset proposal plus `onto-open/references/tasks.md`
with inline Owner/Repo/Cwd/Files/Change/Verify contracts; `onto/references/state-yaml.md`
and `onto-open/references/notes.md` retain their normal structure. A `notes.md` checkpoint
is recommended for any fix that takes more than one sitting. **Commit the
workspace** before the first task in existing mode. In managed mode, checkpoint
manual Markdown with `homonto workspace checkpoint --path changes/<name> --message "Record fix phase"`
before binary mutations and each phase exit; binary mutations checkpoint
automatically. Throughout this preset, records-commit instructions mean this
checkpoint in managed mode; source commits remain in their selected source repo.
Same-repo implementation tasks stay serial. `onto new`
records `phase: open`. The preset skips design, but the binary still walks the
fixed phase sequence `open → design → build → verify → close`: advance
mechanically through the skipped phases. The gates are workflow-aware
(`RequiredArtifacts(phase, "fix")` needs only `proposal.md` + `tasks.md`), so
a fix can leave `open` and `design` without writing a `design.md`. The
dispatcher still derives the *working* phase (build) from the workspace, but
the canonical `phase` field must reach `close` before `onto close` will
archive. Review the completed proposal and record `onto set proposal-approved
<name> "YYYY-MM-DD <scope and reproduction review>"`. Reach build only after
artifacts, isolation, decisions, and required records checkpoints are complete:

The following advance is conditional on recorded open/design. For later-phase
setup repair, skip it and return through the dispatcher after repairing the gap;
reuse isolation and existing decisions rather than resetting defaults.

```
onto set isolation <name> branch|worktree
onto advance <name> --to build      # walks open → design → build, every gate firing
```

Then execute the build. After its tasks and commits are complete, run `onto
advance <name>` only at recorded build to enter verify; if already verify/close,
return through the dispatcher without advancing the later state. Verification selects its scale from the shared
risk check and records a passing report before the final advance into close.

Classify the request from evidence before building. When the requested behavior
already exists and the reproduction demonstrates a regression, proceed as a
fix. If the desired behavior is new or ambiguous, ask the user to choose between
restoring existing behavior and defining a changed contract; only that intent
question justifies interrupting the preset.

### 2. Build — failing test first, always

**A failing test that reproduces the bug is required FIRST, regardless of
the `tdd` decision.** Watch it fail for the expected reason. Then find the
root cause (systematic debugging — reproduce, read the whole error, trace
data flow; no fix before the root cause is identified), apply the minimal
fix, watch the test pass, run the surrounding tests. One commit per task.

### 3. Verify

Follow `onto-verify`'s scale and risk check. The preset is light only when it
stays within the measured size limit and touches no security-sensitive surface;
otherwise it uses full verification and its required skeptics. The bug's
reproduction is the core scenario: demonstrate it no longer occurs, with the
literal command + output in `<workflow-root>/changes/<name>/verification.md`
(template: `onto-verify/references/verification.md`), plus regression-suite
results. On failure, fix by default; ask only before accepting a lower-severity
deviation. Follow onto-verify's report-before-receipts ordering: finalize and
de-slop the report, checkpoint manual report/notes in managed mode, record current
scenario claims, then `onto set verify-result <name> pass|fail`. On pass at recorded verify run
`onto advance <name> --dir "<configRoot>"`; state checkpoints are automatic.
Existing mode retains manual report/state and phase-state commits.
If recorded close, skip advance and return through the dispatcher. A repaired
report never authorizes an extra phase hop.

### 4. Close

Same obligations as `onto-close` — lint (`onto-close/references/
lint-checklist.md`), spec deltas merged if any requirement changed, close plan
validated and recorded, archive to `<workflow-root>/changes/archive/YYYY-MM-DD-<name>/`,
then integrate per repository policy. The preset has no guides obligation; a
legacy `guides: pending` value must still be resolved before archive.

## Upgrade rules

The moment ANY of these becomes true, stop preset implementation and upgrade
automatically to the full workflow:

- the fix touches **more than 5 non-test files** (the mandatory failing test
  never counts toward the trigger; aligned with tweak's limit so a fix never
  carries more ceremony than a same-sized feature)
- architecture or schema changes (new modules, interfaces, dependencies)
- the fix introduces a **new public API**
- the fix scope exceeds a single function/module

On upgrade, run `onto set workflow <name> full`, annotate the proposal's first
line to `Preset: fix (upgraded to full YYYY-MM-DD)`, and create `design.md` from
the full template. Expand the reduced proposal to the full proposal structure,
preserving confirmed scope, scenarios, markers, and Grounding. Mark the design
with `Status: Under revision`. That marker drives working
phase derivation to design without moving the canonical phase backward. Route
through `/onto` to backfill the design, then continue. Ask only if the discovered
work exceeds the user's requested product scope. Never keep patching past a
trigger "because it's almost done".

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace + reproduction establish a bug fix with no new
      design, any genuine behavior ambiguity resolved, workspace committed;
      `onto set isolation <name> branch|worktree` recorded and created; advanced to build via
      `onto advance <name> --to build` only from recorded open/design (gated hops, no design.md needed
      for a fix)
- [ ] Build: failing test seen failing, root cause stated, fix committed,
      test seen passing, tree clean; advanced build → verify only from recorded build
- [ ] Verify: `verification.md` with reproduction evidence + regression
      results; `verify.result` set via `onto set verify-result`; advanced
      verify → close via `onto advance <name>` only from recorded verify; later-phase
      repair preserves phase and returns through the dispatcher; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), preset guides unset or any
      carried obligation resolved, `onto merge-deltas` run, `close.merged` set, close plan
      validated **before** any spec/ADR mutation, close prep committed, archived
      in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
