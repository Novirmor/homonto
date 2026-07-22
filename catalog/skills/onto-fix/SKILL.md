---
name: onto-fix
description: onto preset — bug fix. Use for behavior fixes that need no new capability design — open-lite, then build starting from a failing test that reproduces the bug, verify, close; upgrades to the full workflow when scope grows.
---

# onto-fix — Preset: Bug Fix

Fast path for fixing broken behavior: **open-lite → build → verify → close**.
Skips the design phase — which is exactly why the upgrade rules below are
non-negotiable.

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
suspected blast radius. Create `docs/changes/<name>/` with:

- Create the workspace via `onto new <name> --workflow fix` (`onto new`
  creates `onto-state.yaml` carrying `workflow: fix`, `phase: open`,
  `created`, and empty `proposal.md`/`tasks.md`). Then:
  - `onto set base-ref <name> "$(git rev-parse HEAD)"`
  - `onto set guides <name> pending`
  - default the decisions (presets enter build directly): `onto set isolation
    <name> branch`, `onto set build-mode <name> direct`, **`onto set tdd-mode
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

No full design and no plan.md required. Branch: `fix/YYYYMMDD/<name>`.
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
archive. Run the advances up front, right after `onto new`:

```
onto set isolation <name> branch    # required before entering build
onto advance <name>                 # open  → design (no design.md needed for fix)
onto advance <name>                 # design → build
```

Then execute the build. After verify, advance once more into close.

> **GATE (open-lite scope):** presets skip design, so the fix-vs-full
> choice is the one decision that removes a phase. Confirm it: state the
> reproduction and that this is a bug fix needing no new design, and get
> the user's acknowledgement before building. A behavior *change* dressed
> as a fix belongs in the full workflow — this gate is where that gets
> caught. May be pre-authorized by a directive that named the preset.

### 2. Build — failing test first, always

**A failing test that reproduces the bug is required FIRST, regardless of
the `tdd` decision.** Watch it fail for the expected reason. Then find the
root cause (systematic debugging — reproduce, read the whole error, trace
data flow; no fix before the root cause is identified), apply the minimal
fix, watch the test pass, run the surrounding tests. One commit per task.

### 3. Verify (light)

`verify.mode: light` unless upgraded. The bug's reproduction is the core
scenario: demonstrate it no longer occurs, with the literal command +
output in `docs/changes/<name>/verification.md` (template:
`onto-verify/references/verification.md`), plus regression-suite results.
One adversarial skeptic (`onto-skeptic`, conformance lens) is optional in light
mode (protocol: `onto-verify/references/adversarial.md`); record a skip. Failure → same
gate as the full workflow (fix or accept-deviation, fresh user input).

### 4. Close

Same obligations as `onto-close` — lint (`onto-close/references/
lint-checklist.md`), spec deltas merged if any requirement changed, guides
checked (`updated` or `"waived: <reason>"`), final
confirmation, archive to `docs/changes/archive/YYYY-MM-DD-<name>/`, ship
handoff offered.

## Upgrade rules

> **GATE (upgrade):** the moment ANY of these becomes true, pause, explain
> the trigger, and require fresh user confirmation to upgrade:
>
> - the fix touches **more than 5 non-test files** (the mandatory failing
>   test never counts toward the trigger; aligned with tweak's limit so a
>   fix never carries more ceremony than a same-sized feature)
> - architecture or schema changes (new modules, interfaces, dependencies)
> - the fix introduces a **new public API**
> - the fix scope exceeds a single function/module
>
> On confirmed upgrade: **annotate the proposal's first line to `Preset: fix
> (upgraded to full YYYY-MM-DD)`** — the dispatcher re-derives `workflow: full`
> from that marker (there is no `onto set workflow`; the marker is the
> authority the state-rebuild reads). Then run `onto advance <name>` to reach
> design and route through `/onto` to backfill it. Never keep patching past a
> trigger "because it's almost done".

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace + reproduction confirmed by the user, scope
      gate acknowledged (bug fix, no new design), workspace committed;
      `onto set isolation <name> branch` recorded; advanced open → design
      → build via `onto advance <name>` (mechanical, no design.md needed
      for a fix)
- [ ] Build: failing test seen failing, root cause stated, fix committed,
      test seen passing, tree clean (workspace docs committed)
- [ ] Verify: `verification.md` with reproduction evidence + regression
      results; `verify.result` set via `onto set verify-result`; advanced
      verify → close via `onto advance <name>`; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), guides resolved (fix preset
      needs no guides), `onto merge-deltas` run, `close.merged` set, final
      gate **before** any spec/ADR mutation, close prep committed, archived
      in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
