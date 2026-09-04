---
name: onto-fix
description: onto preset — bug fix. Use for behavior fixes that need no new capability design — open-lite, then build starting from a failing test that reproduces the bug, verify, close; upgrades to the full workflow when scope grows.
---

# onto-fix — Preset: Bug Fix

Fast path for fixing broken behavior: **open-lite → build → verify → close**.
Skips the design phase — which is exactly why the upgrade rules below are
non-negotiable.
Apply the dispatcher's shared autonomous workflow policy throughout and continue
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
  a fresh session must not re-run earlier steps). Derive the phase, then
  enter at the matching step — never above it:

  | Derived phase | Enter at |
  |---|---|
  | build (workspace exists) | step 2, first unchecked task — `git status` first (reconcile any partial work), never redo a committed task |
  | verify | step 3 |
  | close | step 4 |

  Only a brand-new request with no workspace starts at step 1.

## Steps

### 1. Open-lite

Minimal clarification: reproduction steps, expected vs actual behavior,
suspected blast radius. Create `<workflow-root>/changes/<name>/` with:

- Create the workspace via `onto new <name> --workflow fix`, adding one `--repo
  <alias>` for each declared sibling the requested fix changes (`onto new`
  creates `onto-state.yaml` carrying `workflow: fix`, `phase: open`,
  `created`, and empty `proposal.md`/`tasks.md`). Then:
  - `onto set base-ref <name> "$(git rev-parse HEAD)"`
  - `onto set base-branch <name> "$(git branch --show-current)"`; if HEAD is
    detached, derive the intended integration branch from repository policy
    before recording it
  - `onto set deps <name> --dep <a> --dep <b>` for prerequisite active changes
    identified from the request or repository (omit when there are none)
  - default the decisions (presets enter build directly): `onto set isolation
    <name> branch|worktree`, `onto set build-mode <name> direct`, **`onto set tdd-mode
    <name> tdd`** — a fix's whole method is a failing test that reproduces the
    bug first, so its build runs the TDD branch; never default a fix to
    `tdd-mode direct`.
- `proposal.md` — a `Preset: fix` line at column 0 under the title (the
  state rebuild greps `^Preset:`), then the bug (link the issue if any),
  reproduction, expected behavior, fix scope
- `tasks.md` — short checklist (reproduce → fix → regression). The
  checklist is live during the fix: in-scope discovered work is APPENDED
  as a new unchecked item before its code is written, checked off as its
  commit lands — never done silently (scope-exceeding work hits the
  upgrade gate instead)

No full design and no plan.md required. Choose `worktree` for unrelated dirt or
concurrent work and `branch` for a clean serial change. Create the selected
isolation and use branch `fix/YYYYMMDD/<name>` before implementation.
Templates: reuse the full-workflow references (`onto/references/state-yaml.md`,
`onto-open/references/{proposal,tasks,notes}.md`) — a `notes.md` checkpoint
is recommended for any fix that takes more than one sitting. **Commit the
workspace** before the first task (so `base_ref` and recovery hold). `onto new`
records `phase: open`. The preset skips design, but the binary still walks the
fixed phase sequence `open → design → build → verify → close`: advance
mechanically through the skipped phases. The gates are workflow-aware
(`RequiredArtifacts(phase, "fix")` needs only `proposal.md` + `tasks.md`), so
a fix can leave `open` and `design` without writing a `design.md`. The
dispatcher still derives the *working* phase (build) from the workspace, but
the canonical `phase` field must reach `close` before `onto close` will
archive. Reach build in one gated call, right after `onto new`:

```
onto set isolation <name> branch|worktree
onto advance <name> --to build      # walks open → design → build, every gate firing
```

Then execute the build. After its tasks and commits are complete, run `onto
advance <name>` to enter verify. Verification records `verify.scale: light` and
a passing report before the final advance into close.

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

### 3. Verify (light)

Run `onto set verify-scale <name> light`. The bug's reproduction is the core
scenario: demonstrate it no longer occurs, with the literal command +
output in `<workflow-root>/changes/<name>/verification.md` (template:
`onto-verify/references/verification.md`), plus regression-suite results.
One adversarial skeptic (`onto-skeptic`, conformance lens) is optional in light
mode (protocol: `onto-verify/references/adversarial.md`); record a skip. On
failure, fix by default; ask only before accepting a lower-severity deviation.
Record the outcome with `onto set verify-result <name> pass|fail`. On pass,
commit the report and state, then run `onto advance <name>` to enter close.

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
the full template with `Status: Under revision`. That marker drives working
phase derivation to design without moving the canonical phase backward. Route
through `/onto` to backfill the design, then continue. Ask only if the discovered
work exceeds the user's requested product scope. Never keep patching past a
trigger "because it's almost done".

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace + reproduction establish a bug fix with no new
      design, any genuine behavior ambiguity resolved, workspace committed;
      `onto set isolation <name> branch|worktree` recorded and created; advanced to build via
      `onto advance <name> --to build` (gated hops, no design.md needed
      for a fix)
- [ ] Build: failing test seen failing, root cause stated, fix committed,
      test seen passing, tree clean; advanced build → verify
- [ ] Verify: `verification.md` with reproduction evidence + regression
      results; `verify.result` set via `onto set verify-result`; advanced
      verify → close via `onto advance <name>`; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), preset guides unset or any
      carried obligation resolved, `onto merge-deltas` run, `close.merged` set, close plan
      validated **before** any spec/ADR mutation, close prep committed, archived
      in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
