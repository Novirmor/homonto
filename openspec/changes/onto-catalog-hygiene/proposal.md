# onto-catalog-hygiene

## Why

A harsh review of the onto framework's shipped catalog content (2026-07-22)
found prose defects that contradict either the framework's own doctrine or the
v0.8.0 release:

- `onto-explorer` is denied bash while the framework's grounding pillar routes
  all investigation through it — `graphify query`, `codegraph explore`, and
  `git log` are shell commands the explorer cannot run. The capability profile
  contradicts the delegation table.
- Tier vocabulary survives in shipped prose ("trivial-tier model",
  "review-tier model", the dispatcher's "Role/model" column) one release after
  v0.8.0 removed tiers. Shipped content contradicts the config surface.
- The no-slop numeric self-score (`<total>/50`, threshold 35) is graded by the
  same model that wrote the prose; it never fires and decorates every phase
  with fake rigor.
- Four smaller defects: the primary agent's `steps: 120` budget has no
  exhaustion guidance; the verify failure gate both offers and forbids
  proposing deviation-acceptance; the rtk preflight warns on every dispatch
  forever; the skeptic count is fixed regardless of change size; the `onto`
  primary agent body duplicates the dispatcher's doctrine and drifts.

## What Changes

- `catalog/subagents/onto-explorer.md`: drop `bash: false` (explorer keeps
  read-only, no-dialogs, no-spawn; gains shell for grounding tools and git).
- Purge tier vocabulary from catalog prose: `onto-explorer.md`,
  `onto-reviewer.md`, `onto-skeptic.md` frontmatter comments,
  `onto-implementer.md` comment, `onto/SKILL.md` delegation table ("Role/model"
  column), `onto-build/references/subagent-protocol.md`.
- Drop the numeric no-slop self-score everywhere it is mandated (dispatcher
  §Prose discipline, phase exit checklists); keep the edit pass itself.
- `catalog/subagents/onto.md`: add a step-exhaustion note (resume from
  tasks.md); slim the body to defer to the dispatcher skill instead of
  restating it.
- `onto-verify/SKILL.md`: resolve the failure-gate contradiction (acceptance
  is presented as an option with fix recommended; the "never propose" line is
  reworded to "never recommend"); add a skeptic scaling note for large full
  verifies.
- `onto/SKILL.md`: rtk warning recorded once per change in `notes.md`, not
  re-warned every dispatch.
- Mirror the shared defects in the `to` framework twins (same review findings,
  same shipped catalog): `to-explorer.md` gains bash, tier vocabulary purged
  from `to-explorer.md` / `to-reviewer.md` / `to-skeptic.md`, and the numeric
  no-slop score dropped from `to-no-slop`, `to/SKILL.md`, `to-plan/SKILL.md`.
- Bump both framework versions (`catalog/frameworks/{onto,to}/framework.toml`).

## Capabilities

### New Capabilities

None — this change edits shipped prose and one capability flag; no new
spec-level behavior is introduced.

### Modified Capabilities

None — `openspec/specs/` holds only `agent-models`, which does not govern
per-agent tool permissions or skill prose.

## Impact

- `catalog/subagents/onto*.md` (5) + `to-explorer/reviewer/skeptic.md` (3),
  `catalog/skills/onto/SKILL.md`, `onto-verify`, `onto-build/references/subagent-protocol.md`,
  every exit checklist naming the no-slop score (onto-open, onto-design,
  onto-verify, onto-close, onto-fix, onto-tweak, onto-no-slop; to, to-plan,
  to-no-slop), both `framework.toml` versions.
- No Go code.
- Users: explorer agents re-render with bash allowed on next
  `homonto update`/`apply`; prose changes are advisory content.
