# Reduced preset proposal

Fix and tweak use this structure, not the full proposal template. All headings
below are required; keep content short and specific. Close lint checks this
template for an unupgraded preset, and the full template after upgrade.

```markdown
# Proposal: <change-name>
Preset: fix

## Why
<bug and reproduction, expected versus actual; for tweak, what and why>

## What Changes
<bounded outcome and files/modules affected>

## Non-Goals
<explicit boundary>

## Capability Impact
<New/Modified capabilities with delta paths, or explicit no-spec-level change
and why. A tweak cannot change an existing spec requirement or add a capability.>

## Acceptance Scenarios
- <stable descriptive ID>: <input/precondition, action, expected observable result>
- <regression or important edge case>: <action and expected result>

## Grounding
<actual queries or direct file paths read and what established the scope>
```

Use `Preset: tweak` for tweaks. For fix, one core scenario must reproduce the
bug before the fix and pass afterward. Every scenario is verified in the report,
even when there are no spec deltas. For no-spec fix/tweak structured evidence,
declare each stable `Scenario-ID: <id>` in `tasks.md` or `verification.md`, using
the same scenario identity as the proposal. The binary supports those declarations;
do not create fake delta specs or rely on proposal-only IDs for evidence lookup.
Optional `Depends-on:` follows the full proposal marker rules. `Closes: #N` is
allowed only for a confirmed issue in the publication origin repository; keep
other issue identities as ordinary canonical URLs, not parser extensions.

Preset `tasks.md` uses the canonical dotted IDs and unique `[trace #N]` markers.
With no `plan.md`, put Owner, Repo, Cwd, Files, Change, and Verify (including its
passing signal) under each task in `tasks.md`. Owner is `coordinator` for records
tasks and the chosen build lane for source tasks. No empty placeholder checklist
counts as setup. Notes are optional, but when present use the notes template.
