# Close-phase lint checklist

Agent-run checks (grep/read — no scripts), staged: sections run at the
points onto-close names (§0–2 before the merge, §3 after it, §4 before
archiving). Findings block the archive step exactly like the guides
obligation: fix them or stop.

## 0. Delta coverage (behavior changes have specs — the central check)

- [ ] Every capability the proposal's Capability Impact marks **New** or
      **Modified** has a matching workspace `specs/<capability>.md` delta.
      A capability declared changed with no delta is a blocking finding —
      not a checkbox to wave through.
- [ ] Diff the change against its `base_ref` (`git diff --stat
      <base_ref>..HEAD`). If it touched product source but the workspace
      has **zero** `specs/*.md` deltas, that is a finding: either the
      change has no spec-level behavior (state that explicitly, in the
      close summary, as a deliberate no-spec change) or a delta is missing.
      Silence here is how behavior ships with no spec and no scenario —
      the failure the whole workflow exists to prevent.

## 1. Delta spec format (each workspace `specs/<capability>.md`)

- [ ] Section headings are only `## ADDED Requirements`,
      `## MODIFIED Requirements`, `## REMOVED Requirements`,
      `## RENAMED Requirements` — nothing else, empty sections omitted
- [ ] Every `### Requirement:` block **in ADDED/MODIFIED sections** has
      SHALL or MUST in its first non-empty line after the heading
      (REMOVED bodies are removal rationales; RENAMED has no bodies —
      neither is subject to this rule)
- [ ] **Every** `#### Scenario:` block has WHEN and THEN bullets (GIVEN
      optional), and each ADDED/MODIFIED requirement has ≥1 scenario
- [ ] MODIFIED/REMOVED/RENAMED names match the living spec **exactly**
      (grep the living file for each name) — except a MODIFIED name may
      instead match the TO name of a RENAMED entry in the same delta
- [ ] A MODIFIED/REMOVED/RENAMED section in a delta whose capability has
      **no living spec file** is itself a finding
- [ ] RENAMED entries are `- FROM:` / `  TO:` pairs

## 2. Workspace state

- [ ] `onto-state.yaml` parses as YAML; enum fields hold allowed values and
      typed fields hold their types (`deps` a list, `observed.metrics` a
      map, counters numeric — schema: `onto/references/state-yaml.md`)
- [ ] `verification.md` has a current `Result:` line
- [ ] Every ADR draft has `**Status:**`, `**Date:**`, `**Change:**` fields
- [ ] Every template-based artifact **that exists** follows its
      template's section structure — proposal, design, tasks, notes,
      plan, verification — checked against their references (deviation
      anywhere is a finding; presets legitimately lack design/plan and
      possibly notes)
- [ ] **Grounding is recorded, not blank**: the proposal's `## Grounding`
      section names the code-intelligence queries run or the recorded
      fallback (e.g. "index declined — direct file reading"); a full
      change's `design.md` `## Grounding` likewise. An empty heading is a
      finding — a silently ungrounded change is what this catches (the
      design rested on real reads or it did not, and the record says which)

## 3. Post-merge (run AFTER the spec merge, before archive)

- [ ] Living specs contain **no** delta-only section headings:
      `grep -nE '^## (ADDED|MODIFIED|REMOVED|RENAMED) Requirements' docs/specs/*.md`
      → no matches outside `docs/specs/README.md` (the README legitimately
      documents the section names; prose mentions anywhere are fine — the
      check is heading-anchored)
- [ ] **No duplicated requirements**: in every touched living spec each
      `### Requirement: <name>` heading appears once — a MODIFIED that
      appended instead of replacing, or a document-order merge, leaves the
      old block beside the new one and shows up here as a repeat
- [ ] Merged requirements read as current truth — no change-log language
- [ ] Scenario structure intact in every touched living spec

## 4. Pre-archive

- [ ] `guides` is not `pending` (resolved in the guides-obligation step —
      checked here because it cannot be satisfied before that step runs)
- [ ] No unresolved `DEFERRED to close:` markers remain in `tasks.md` —
      deferred tasks are executed during close, before the final
      confirmation; archiving one undone is prohibited
- [ ] No live doc (README, docs/guides, docs/specs, docs/adr, skills)
      references a path this change moved or deleted — archives are exempt
      (history may cite old paths)
