---
name: onto-tweak
description: onto preset — small non-bug change. Use for copy, configuration, documentation, or prompt tweaks, and for small features within tweak limits (≤5 files, no new capability, no existing-spec requirement change) — open-lite, lightweight build, light verify, close; upgrades to the full workflow when scope grows.
---

# onto-tweak — Preset: Small Change

Fast path for small non-bug changes (copy, config values, docs, prompts)
and for small features that stay within the tweak limits:
**open-lite → lightweight build → light verify → close**. Skips design and
the full plan — bounded by strict upgrade rules.
Apply the shared [autonomous workflow policy](../homonto/references/autonomy.md),
including workspace roots and dirty-work decisions, even on direct entry. Continue
through the entire preset unless the user names an endpoint or asks to pause.

## Entry check

- A small, local, non-bug change request, or an active change with
  `workflow: tweak`. This preset owns the change's whole lifecycle.
- Broken behavior → `onto-fix`. **Small features are tweak territory** when
  ALL of: ≤5 files touched (test files excluded), no new capability (no new
  `<workflow-root>/specs/` file), and no existing spec's requirements change.
  Structural work or anything introducing a new capability → full workflow
  via `onto-open`.
- Read `notes.md` at entry when present (recommended for any tweak that
  spans sittings). If any skill's `references/` directory is missing, note
  the gap and fall back to the SKILL.md tables, continue.
- **Resume map** (the dispatcher routes every phase of a tweak change here):

  | Recorded/setup or derived phase | Enter at |
  |---|---|
  | recorded open/design, or missing proposal review/isolation/task contracts | step 1 setup resume; preserve state, fill only missing artifacts/decisions, never rerun `new` |
  | build with setup complete | step 2, first unchecked task; inspect partial work, never redo a committed task |
  | verify | step 3 |
  | close | step 4 |

  Read recorded phase before derived phase. Only a brand-new request runs
  `onto new`; an empty scaffold deriving build is not completed open/setup.
  Setup repair at recorded build/verify/close fills only missing artifacts or
  decisions and preserves the recorded phase. Run `advance --to build` only
  when recorded phase is open or design, never after it already reached build.
  Report-only repair refreshes report/evidence as needed, without fake phase hops.

## Steps

### 1. Open-lite

Use [the reduced preset template](../onto-open/references/preset-proposal.md):
`Preset: tweak`, Why, What Changes, Non-Goals, Capability Impact, Acceptance
Scenarios, and non-empty Grounding. Close lint uses that template, not the full
proposal. Use canonical dotted tasks with `[trace #N]` and inline
Owner/Repo/Cwd/Files/Change/Verify contracts when there is no plan.
Initialize an authorized empty managed records root before any scaffold write.
Choose isolation before `new`; allocate schema-2 bindings immediately after
creation, before any records/source commit. Resume existing isolation; never
retarget a frozen base. Create the workspace via
`onto new <name> --workflow tweak`, adding one `--repo <alias>` for each declared
source in scope and `--dir "<configRoot>"`. In schema 2, pass repeated
`--base <alias>=<local-branch>` at creation for alternative bases, such as
`--base api=main --base web=develop`. Otherwise each source's current committed
HEAD and local branch are frozen. Select `api=main` here to isolate from `main`
without switching or cleaning a dirty feature checkout. Creation freezes
`repo_bases`; validate each immutable anchor with `onto set base-ref <name> <commit>
--repo <alias> --dir "<configRoot>"` and `onto set base-branch <name> <branch>
--repo <alias> --dir "<configRoot>"`. These setters do not retarget bases;
worktree creation must match the frozen commit and target, never config/records
HEAD. Legacy combined mode retains scalar setters.
Record any prerequisite changes with `onto set deps`. Record the default
decisions: `onto set isolation <name> branch|worktree`, `onto set build-mode
<name> direct`, `onto set tdd-mode <name> direct`. Inspect before writes and
resolve the shared preserve/isolate/cleanup choice once. Create a registered
worktree when isolation is chosen; otherwise create a safe source branch
`tweak/YYYYMMDD/<name>`. **Commit the
workspace** before the first task in existing mode. In managed mode, checkpoint
manual Markdown with `homonto workspace checkpoint --path changes/<name> --message "Record tweak phase"`
before binary mutations and each phase exit; binary mutations checkpoint
automatically. Throughout this preset, records-commit instructions mean this
checkpoint in managed mode; source commits remain in their source repo and
same-repo tasks stay serial. `onto new` records `phase: open`. The preset
skips design, but the binary still walks the fixed phase sequence
`open → design → build → verify → close`. The gates are workflow-aware
(`RequiredArtifacts(phase, "tweak")` needs only `proposal.md` + `tasks.md`),
so a tweak reaches build without a `design.md` — in one gated call, right
after completing artifacts, isolation, decisions, and required checkpoints.
Review the completed proposal and record `onto set proposal-approved <name>
"YYYY-MM-DD <bounded scope review>"` before advancing:

The following advance is conditional on recorded open/design. For later-phase
setup repair, skip it and return through the dispatcher after repairing the gap;
reuse isolation and existing decisions rather than resetting defaults.

```
onto advance <name> --to build    # walks open → design → build, every gate firing
```

Then execute the build. Once every task is committed, run `onto advance <name>`
only at recorded build to enter verify. If already verify/close, return through
the dispatcher without advancing the later state. After verification is recorded
and committed, advance into close only from recorded verify.

Classify the request from repository evidence before building. Proceed when it
objectively fits the tweak limits. Ask only when whether the request changes a
capability or existing requirement depends on missing product intent.

### 2. Lightweight build

No `plan.md` required. Still binding:

- one commit per task, checked off in `tasks.md` as it lands
- the checklist is live: in-scope discovered work is APPENDED to `tasks.md`
  as a new unchecked item before its code is written — never done silently
- on ANY failure: systematic debugging — root cause before any fix
- stay inside the tweak's stated scope; anything more hits the upgrade gate

### 3. Verify

Follow `onto-verify`'s scale and risk check. The preset is light only when it
stays within the measured size limit and touches no security-sensitive surface;
otherwise it uses full verification and its required skeptics. Demonstrate the
changed behavior/content with a fresh command + output (render the doc, run the
config consumer, show the diff taking effect) and run the regression suite.
Write `<workflow-root>/changes/<name>/verification.md` (template:
`onto-verify/references/verification.md`) — brief is fine, absent is not.
Follow onto-verify's report-before-receipts ordering: finalize and de-slop the
report, checkpoint manual report/notes in managed mode, record current scenario
claims, then `onto set verify-result <name> pass|fail`. Fix failures by default
and ask only before accepting a lower-severity deviation. On pass at recorded verify run
`onto advance <name> --dir "<configRoot>"`; state checkpoints are automatic.
Existing mode retains manual report/state and phase-state commits.
If recorded close, skip advance and return through the dispatcher. A repaired
report never authorizes an extra phase hop.

### 4. Close

Full `onto-close` execution: lint, merge any spec deltas, validate and record the
close plan, archive, then integrate per repository policy. The preset has no
guides obligation; resolve a carried legacy `guides: pending` value if present.

## Upgrade rules

Upgrade automatically to the full workflow when ANY of:

- the change touches **more than 5 files** (test files excluded — the entry
  limit is ≤5, so exactly 5 is still a tweak)
- cross-module coordination is required
- **5+ new test cases** are needed
- config **keys are added or removed** (value changes are fine)
- a new capability emerges
- an existing spec's requirements are affected

On upgrade, run `onto set workflow <name> full`, annotate the proposal's first
line to `Preset: tweak (upgraded to full YYYY-MM-DD)`, and create `design.md`
from the full template with `Status: Under revision`. Route through `/onto` to
expand the reduced proposal to the full template, preserving confirmed scope,
scenarios, markers, and Grounding, and
backfill design without moving the canonical phase backward. Ask only if the
discovered work exceeds the user's requested product scope.

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace exists, tweak limits established, any genuine scope
      ambiguity resolved, workspace
      committed; advanced to build via `onto advance <name> --to build` only from recorded open/design
      (gated hops, no design.md needed for a tweak)
- [ ] Build: tasks checked + committed one by one, tree clean (workspace
      docs committed); advanced build → verify only from recorded build
- [ ] Verify: `verification.md` with fresh evidence + regression results;
      `verify.result` set via `onto set verify-result`; advanced verify →
      close via `onto advance <name>` only from recorded verify; later-phase repair
      preserves phase and returns through the dispatcher; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), preset guides unset or any
      carried obligation resolved, `onto merge-deltas` run, `close.merged` set,
      close plan validated **before** any spec/ADR mutation, close prep committed,
      archived in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
