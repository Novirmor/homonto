---
name: onto-light
description: onto light preset — bounded bug fixes and small non-bug edits (copy, config, docs, prompts, a small feature). Use when onto light is explicitly selected or an active light change resumes — open-lite, build, verify without skeptics, close; escalates to the full workflow when scope or risk grows.
---

# onto-light — Preset: Light

Fast path for bounded work: **open-lite → build → verify → close**. Light skips
design and plan, and it never runs a skeptic. That is its main difference from
full onto, and it is why the escalation rules below are non-negotiable: work
that needs design, a contract change, or independent adversarial review is full
work.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry. Continue
through the entire preset unless the user names an endpoint or asks to pause.
Follow the [delivery loop](../onto/references/delivery.md), including endpoint
capture and early delivery checks during setup, bounded repairs and PR readiness.

## Entry check

- A new request the user selected for light, or an active change with
  `workflow: light`. This preset owns the change's whole lifecycle; the
  dispatcher routes every phase of a light change here.
- Validate the shared [eligibility table](../homonto/references/workflow-selection.md#eligibility-and-escalation).
  If an explicit light request needs a new contract, design, or a
  security-sensitive surface, explain the mismatch and ask for full or a scope
  decision; never silently replace the user's choice. A capability is a
  behavioral contract, not merely the existence of a spec file.
- Read `notes.md` at entry when present. If any skill's `references/`
  directory is missing, degrade per the dispatcher rule: note the gap and
  fall back to the SKILL.md tables, continue.
- **Resume map** (a fresh session must not re-run completed steps). Read
  recorded phase and setup before using derived phase:

  | Recorded/setup or derived phase | Enter at |
  |---|---|
  | recorded open/design, or missing proposal review/isolation/task contracts | step 1 setup resume; preserve state, fill only missing artifacts/decisions, never rerun `new` |
  | build with setup complete | step 2, first executable unchecked task under recorded dependencies; reconcile partial work, never redo a committed task |
  | verify | step 3 |
  | close | step 4 |
  | done | delivery loop's endpoint check; do not rerun archive |

  Only a brand-new request runs `onto new`. An existing empty light scaffold
  may derive build while recorded open; it still needs setup and gated advancement.
  Setup repair at recorded build/verify/close fills only missing artifacts or
  decisions and preserves the recorded phase. Run `advance --to build` only
  when recorded phase is open or design, never after it already reached build.
  Report-only repair refreshes report/evidence as needed, without fake phase hops.

## Steps

### 1. Open-lite

**Classify the request from repository evidence first.** This sets the build
method, not the workflow:

| Kind | Evidence | `tdd-mode` |
|---|---|---|
| Bug | The requested behavior already exists and a reproduction shows it broken | `tdd`; a failing reproduction test comes first |
| Testable change | New or changed logic a test can observe, within the preset limits | `tdd` |
| Content | Copy, configuration values, documentation, prompts | `direct` |

If whether the request restores existing behavior or defines a changed contract
depends on missing product intent, ask that one question; it is the only intent
question that justifies interrupting the preset. Clarify minimally: for a bug,
the reproduction steps, expected versus actual behavior and suspected blast
radius; otherwise what changes and why.

Before any scaffold, initialize the authorized empty managed records root.
Reuse the user's recorded isolation choice, or ask once before `new` under the
shared execution policy; allocate schema-2 bindings immediately after
creation, before any records/source commit. Resume validated isolation instead
of allocating again; a frozen-base mismatch blocks, never retarget it.

- Create the workspace via `onto new <name> --workflow light`, adding one `--repo
  <alias>` for every selected source in schema 2, with `--dir "<configRoot>"`.
  Add repeated `--base <alias>=<local-branch>` at creation for alternative bases,
  such as `--base api=main --base web=develop`; otherwise each source's current
  committed HEAD and local branch are frozen. To isolate from `main` while on
  a dirty feature branch, select `api=main` here without switching or cleaning
  the original. (`onto new` creates `onto-state.yaml` carrying `workflow: light`,
  `phase: open`, `created`, and empty `proposal.md`/`tasks.md`.) Then:
  - Inspect schema 2 `repo_bases` captured at creation; validate each with
    `onto set base-ref <name> <commit> --repo <alias> --dir "<configRoot>"` and
    `onto set base-branch <name> <branch> --repo <alias> --dir "<configRoot>"`.
    These anchors are immutable; setters do not retarget them after creation.
    Worktree creation must match the frozen commit and target. Never use records
    HEAD as source. Legacy combined mode retains scalar setters.
  - `onto set deps <name> --dep <a> --dep <b>` for prerequisite active changes
    identified from the request or repository (omit when there are none).
  - Default the decisions (light enters build directly): `onto set isolation
    <name> branch|worktree`, `onto set build-mode <name> direct`, and `onto set
    tdd-mode <name> tdd|direct` from the classification. Never default a bug to
    `tdd-mode direct`.
- `proposal.md` uses [the reduced preset template](../onto-open/references/preset-proposal.md):
  `Preset: light`, Why, What Changes, Non-Goals, Capability Impact, Acceptance
  Scenarios, and non-empty Grounding. Close lint uses that template.
- `tasks.md` uses canonical dotted IDs with `[trace #N]` and inline
  Owner/Repo/Cwd/Files/Change/Verify contracts (template:
  `onto-open/references/tasks.md`), for example reproduce → fix → regression.
  The checklist is live: in-scope discovered work is APPENDED as a new unchecked
  item before its code is written and checked off as its commit lands, never
  done silently. Scope-exceeding work hits the escalation gate instead.

No design and no plan.md. Inspect dirt and resolve the shared
preserve/isolate/cleanup choice once before writes. Use registered `worktree`
bindings when isolation is chosen and `branch` for a safe serial change. Create
the selected isolation and use branch `light/YYYYMMDD/<name>` before
implementation. `onto/references/state-yaml.md` and
`onto-open/references/notes.md` retain their normal structure; a `notes.md`
checkpoint is recommended for any light change that spans sittings. **Commit
the workspace** before the first task in existing mode. In managed mode,
checkpoint manual Markdown with `homonto workspace checkpoint --path
changes/<name> --message "Record light phase"` before binary mutations and each
phase exit; binary mutations checkpoint automatically. Throughout this preset,
records-commit instructions mean this checkpoint in managed mode; source commits
remain in their selected source repo. Same-repo implementation tasks stay serial.

`onto new` records `phase: open`. Light skips design, but the binary still walks
the fixed phase sequence `open → design → build → verify → close`. The gates are
workflow-aware (`RequiredArtifacts(phase, "light")` needs only `proposal.md` +
`tasks.md`), so a light change leaves `open` and `design` without a `design.md`.
The dispatcher derives the *working* phase (build) from the workspace, but the
canonical `phase` must reach `close` before `onto close` will archive. Review the
completed proposal and record `onto set proposal-approved <name> "YYYY-MM-DD
<bounded scope review>"`. Reach build only after artifacts, isolation, decisions,
and required records checkpoints are complete.

The following advance is conditional on recorded open/design. For later-phase
setup repair, skip it and return through the dispatcher after repairing the gap;
reuse isolation and existing decisions rather than resetting defaults.

```
onto set isolation <name> branch|worktree
onto advance <name> --to build      # walks open → design → build, every gate firing
```

Then execute the build. After its tasks and commits are complete, run `onto
advance <name>` only at recorded build to enter verify; if already verify/close,
return through the dispatcher without advancing the later state.

### 2. Build

Follow [task execution and evidence](../homonto/references/execution.md) for
reviewable outcomes, focused commits, checkoffs only after landing, dispatch
fallback and evidence reuse. On unexpected failure, follow
[systematic debugging](../homonto/references/debugging.md) before attempting a fix.

**A bug starts with a failing reproduction test, regardless of any other
decision.** Watch it fail for the expected reason, find the root cause, apply the
minimal fix, watch the test pass, run the surrounding tests. Preserve the failing
reproduction as evidence; a later green run does not replace it. Other `tdd`
tasks follow `onto-build/references/tdd-protocol.md`; `direct` tasks implement,
then verify.

On verification failure or a defect found before archive, resume this step with
an appended inline Owner/Repo/Cwd/Files/Change/Verify repair task. Keep the light
workflow, existing isolation, TDD mode and the recorded phase. Do not create
`plan.md` or `design.md` for an in-scope repair. Invalidate the old passing result
before repair, then return to step 3 for fresh verification. Escalate only under
the shared eligibility table, not because a check failed.

### 3. Verify — light scale, no skeptic

Light verification never dispatches a skeptic. Run `onto scale <name> --set
--dir "<configRoot>"`. If it measures `full`, or the diff reaches any risk
trigger in `onto-verify`'s scale check (a new capability or a security-sensitive
surface), the change no longer fits light: **escalate** under the rules below.
The binary refuses `verify-scale full` for a light change.

Demonstrate every Acceptance Scenario with fresh command + output in
`<workflow-root>/changes/<name>/verification.md` (template:
`onto-verify/references/verification.md`), plus regression-suite results. For a
bug, the reproduction is the core scenario: show it no longer occurs. For
content, render the doc, run the config consumer, or show the diff taking
effect. Brief is fine, absent is not. Record the Adversarial section as "light:
no skeptic pass" rather than leaving it blank. Fix failures by default; ask only
before accepting a lower-severity deviation. Non-waivable classes (security
defect, data loss, failed core-acceptance scenario) still block.

Follow onto-verify's report-before-receipts ordering: finalize and de-slop the
report, checkpoint manual report/notes in managed mode, record current scenario
claims, then `onto set verify-result <name> pass|fail`. On pass at recorded
verify run `onto advance <name> --dir "<configRoot>"`; state checkpoints are
automatic. Existing mode retains manual report/state and phase-state commits.
If recorded close, skip advance and return through the dispatcher. A repaired
report never authorizes an extra phase hop.

### 4. Close

Same obligations as `onto-close`: lint (`onto-close/references/lint-checklist.md`),
spec deltas merged if any requirement changed, close plan validated and recorded,
archive to `<workflow-root>/changes/archive/YYYY-MM-DD-<name>/`, then integrate
per repository policy. Light has no guides obligation; a carried legacy
`guides: pending` value must still be resolved before archive.

## Escalation rules

Use the shared [eligibility and escalation table](../homonto/references/workflow-selection.md#eligibility-and-escalation)
before selection and throughout the change. Escalate automatically to full when
the work exceeds it, when `onto scale` measures `full`, or when it reaches a
security-sensitive surface. There are no other file, function, or test-count
thresholds in this skill. Test count and a private configuration-key edit alone
do not force escalation; assess behavior, compatibility, and migration obligations.

On escalation, run `onto set workflow <name> full`, annotate the proposal's
first line to `Preset: light (upgraded to full YYYY-MM-DD)`, and create
`design.md` from the full template with `Status: Under revision`. That marker
drives working phase derivation to design without moving the canonical phase
backward. Expand the reduced proposal to the full template, preserving confirmed
scope, scenarios, markers, and Grounding. Route through `/onto` to backfill the
design, invalidating any prior passing report and setting verify-result pending
before design is reconfirmed; full verification then runs its required skeptics.
Ask only if the discovered work exceeds the user's requested product scope.
Never keep patching past a trigger "because it's almost done".

## Exit checklist (per phase, lite)

- [ ] Open-lite: request classified and `tdd-mode` recorded from it, light limits
      established, any genuine intent ambiguity resolved, workspace committed;
      `onto set isolation <name> branch|worktree` recorded and created; advanced
      to build via `onto advance <name> --to build` only from recorded open/design
- [ ] Build: a bug's failing test seen failing, root cause stated, fix committed
      and seen passing; other tasks checked and committed one by one; tree
      clean; advanced build → verify only from recorded build
- [ ] Verify: scale measured light with no risk trigger (else escalated);
      `verification.md` with fresh evidence, regression results, and no skeptic
      pass recorded; `verify.result` set via `onto set verify-result`; advanced
      verify → close only from recorded verify; later-phase repair preserves
      phase and returns through the dispatcher; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), guides unset or any carried
      obligation resolved, `onto merge-deltas` run, `close.merged` set, close plan
      validated **before** any spec/ADR mutation, close prep committed, archived
      in its own commit
- [ ] Prose edited for clarity with `onto-no-slop`; preserve machine-read markers,
      normative wording and literal evidence; no style receipt required
- [ ] Integration, authorized publication and the requested delivery endpoint
      confirmed under the delivery loop; archive alone is not completion
