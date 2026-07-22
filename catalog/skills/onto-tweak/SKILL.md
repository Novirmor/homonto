---
name: onto-tweak
description: onto preset — small non-bug change. Use for copy, configuration, documentation, or prompt tweaks, and for small features within tweak limits (≤5 files, no new capability, no existing-spec requirement change) — open-lite, lightweight build, light verify, close; upgrades to the full workflow when scope grows.
---

# onto-tweak — Preset: Small Change

Fast path for small non-bug changes (copy, config values, docs, prompts)
and for small features that stay within the tweak limits:
**open-lite → lightweight build → light verify → close**. Skips design and
the full plan — bounded by strict upgrade rules.

## Entry check

- A small, local, non-bug change request, or an active change with
  `workflow: tweak`. This preset owns the change's whole lifecycle.
- Broken behavior → `onto-fix`. **Small features are tweak territory** when
  ALL of: ≤5 files touched (test files excluded), no new capability (no new
  `docs/specs/` file), and no existing spec's requirements change.
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
`onto new <name> --workflow tweak`, then `onto set base-ref <name> "$(git
rev-parse HEAD)"`, `onto set guides <name> pending`, and the default decisions:
`onto set isolation <name> branch`, `onto set build-mode <name> direct`, `onto
set tdd-mode <name> direct`. Branch: `tweak/YYYYMMDD/<name>`. **Commit the
workspace** before the first task. `onto new` records `phase: open`. The preset
skips design, but the binary still walks the fixed phase sequence
`open → design → build → verify → close`: advance mechanically through the
skipped phases. The gates are workflow-aware (`RequiredArtifacts(phase,
"tweak")` needs only `proposal.md` + `tasks.md`), so a tweak can leave `open`
and `design` without writing a `design.md`. Run the advances up front, right
after `onto new` and the decision defaults:

```
onto advance <name>    # open  → design (no design.md needed for tweak)
onto advance <name>    # design → build
```

Then execute the build. After verify, advance once more into close.

> **GATE (open-lite scope):** confirm this fits a tweak (small, local, no
> new capability, no existing-spec requirement change) before building —
> the one decision that skips design. May be pre-authorized by a directive
> that named the preset.

### 2. Lightweight build

No `plan.md` required. Still binding:

- one commit per task, checked off in `tasks.md` as it lands
- the checklist is live: in-scope discovered work is APPENDED to `tasks.md`
  as a new unchecked item before its code is written — never done silently
- on ANY failure: systematic debugging — root cause before any fix
- stay inside the tweak's stated scope; anything more hits the upgrade gate

### 3. Light verify

Demonstrate the changed behavior/content with a fresh command + output
(render the doc, run the config consumer, show the diff taking effect) and
run the regression suite. Write `docs/changes/<name>/verification.md`
(template: `onto-verify/references/verification.md`) — brief is fine,
absent is not. One adversarial skeptic (`onto-skeptic`, conformance lens) is
optional (skips recorded).
`verify.result` set; failures hit the same fix-or-accept gate as the full
workflow.

### 4. Close

Full `onto-close` obligations: lint, merge any spec deltas, guides
`updated` or `"waived: <reason>"`, final confirmation, archive, ship handoff
offered.

## Upgrade rules

> **GATE (upgrade):** pause, explain the trigger, and require fresh user
> confirmation to upgrade to the full workflow when ANY of:
>
> - the change touches **more than 5 files** (test files excluded — the
>   entry limit is ≤5, so exactly 5 is still a tweak)
> - cross-module coordination is required
> - **5+ new test cases** are needed
> - config **keys are added or removed** (value changes are fine)
> - a new capability emerges
> - an existing spec's requirements are affected
>
> On confirmed upgrade: **annotate the proposal's first line to `Preset: tweak
> (upgraded to full YYYY-MM-DD)`** — the dispatcher re-derives `workflow: full`
> from that marker (no `onto set workflow` exists). Then run `onto advance
> <name>` to reach design and route through `/onto` to backfill it.

## Exit checklist (per phase, lite)

- [ ] Open-lite: workspace exists, scope gate acknowledged, workspace
      committed; advanced open → design → build via `onto advance <name>`
      (mechanical, no design.md needed for a tweak)
- [ ] Build: tasks checked + committed one by one, tree clean (workspace
      docs committed)
- [ ] Verify: `verification.md` with fresh evidence + regression results;
      `verify.result` set via `onto set verify-result`; advanced verify →
      close via `onto advance <name>`; workspace committed at exit
- [ ] Close: delta coverage checked (lint §0), guides resolved (tweak
      preset needs no guides), `onto merge-deltas` run, `close.merged` set,
      final gate **before** any spec/ADR mutation, close prep committed,
      archived in its own commit
- [ ] onto-no-slop pass run over each prose artifact (proposal,
      verification, new guide prose), noted in `notes.md` (`no-slop: <artifact>
done`); never a
      machine-read marker or a requirement's normative wording
