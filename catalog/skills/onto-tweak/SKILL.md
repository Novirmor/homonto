---
name: onto-tweak
description: onto preset — small non-bug change. Use for copy, configuration, documentation, or prompt tweaks, and for small features within tweak limits (≤5 files, no new capability, no existing-spec requirement change) — open-lite, lightweight build, light verify, close; upgrades to the full workflow when scope grows.
---

# onto-tweak — Preset: Small Change

Fast path for small non-bug changes (copy, config values, docs, prompts)
and for small features that stay within the tweak limits:
**open-lite → lightweight build → light verify → close**. Skips design and
the full plan — bounded by strict upgrade rules.
Apply the dispatcher's shared autonomous workflow policy throughout and continue
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

  | Derived phase | Enter at |
  |---|---|
  | build (workspace exists) | step 2, first unchecked task — `git status` first, never redo a committed task |
  | verify | step 3 |
  | close | step 4 |

  Only a brand-new request with no workspace starts at step 1.

## Steps

### 1. Open-lite

One-paragraph `proposal.md` — a `Preset: tweak` line at column 0 under the
title, then what + why — plus short `tasks.md`. Create the workspace via
`onto new <name> --workflow tweak`, adding one `--repo <alias>` for each declared
sibling in scope. Then record `base-ref` from `git rev-parse HEAD`, `base-branch`
from `git branch --show-current` (derive it from repository policy on detached
HEAD), and any prerequisite changes with `onto set deps`. Record the default
decisions: `onto set isolation <name> branch|worktree`, `onto set build-mode
<name> direct`, `onto set tdd-mode <name> direct`. Choose and create a worktree
for unrelated dirt or concurrent work; otherwise create branch
`tweak/YYYYMMDD/<name>`. **Commit the
workspace** before the first task. `onto new` records `phase: open`. The preset
skips design, but the binary still walks the fixed phase sequence
`open → design → build → verify → close`. The gates are workflow-aware
(`RequiredArtifacts(phase, "tweak")` needs only `proposal.md` + `tasks.md`),
so a tweak reaches build without a `design.md` — in one gated call, right
after `onto new` and the decision defaults:

```
onto advance <name> --to build    # walks open → design → build, every gate firing
```

Then execute the build. Once every task is committed, run `onto advance <name>`
to enter verify. After verification is recorded and committed, advance once more
into close.

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

### 3. Light verify

Run `onto set verify-scale <name> light`.
Demonstrate the changed behavior/content with a fresh command + output
(render the doc, run the config consumer, show the diff taking effect) and
run the regression suite. Write `<workflow-root>/changes/<name>/verification.md`
(template: `onto-verify/references/verification.md`) — brief is fine,
absent is not. One adversarial skeptic (`onto-skeptic`, conformance lens) is
optional (skips recorded).
Set `verify.result` through `onto set verify-result <name> pass|fail`; fix
failures by default and ask only before accepting a lower-severity deviation.
On pass, commit the report and state, then run `onto advance <name>`.

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
backfill design without moving the canonical phase backward. Ask only if the
discovered work exceeds the user's requested product scope.

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace exists, tweak limits established, any genuine scope
      ambiguity resolved, workspace
      committed; advanced to build via `onto advance <name> --to build`
      (gated hops, no design.md needed for a tweak)
- [ ] Build: tasks checked + committed one by one, tree clean (workspace
      docs committed); advanced build → verify
- [ ] Verify: `verification.md` with fresh evidence + regression results;
      `verify.result` set via `onto set verify-result`; advanced verify →
      close via `onto advance <name>`; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), preset guides unset or any
      carried obligation resolved, `onto merge-deltas` run, `close.merged` set,
      close plan validated **before** any spec/ADR mutation, close prep committed,
      archived in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
