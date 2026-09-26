# Centralize workflow policy and preserve preset repairs

- **Status:** Accepted
- **Date:** 2026-09-27

## Context

Repeated policy across skills conflicted on explicit full choices, active repairs,
preset eligibility and unavailable dispatch. Preset verification repairs inherited
full planning, while the minimal workflow could require tooling full onto waived.
Mechanical prose and task quotas added process without establishing correctness.

## Decision

We will give selection, execution, workspace and publication policy one shared
owner, with phase-specific procedures and linked recovery references (ADR 0006).
Honor explicit choices; repair active in-scope defects without another consent
gate. Fix/tweak keep inline tasks and escalate only under the shared eligibility
table. Test count and private config-key edits alone are not escalation triggers.

Both workflows use documented direct fallback for unavailable dispatch, never for
denial or to waive an explicit independent-review requirement. Record the missing
independent pass. Use OpenCode's built-in question tool for every required user
decision. Preserve task identities while recording prerequisite execution order;
prefer cohesive commits and candidate-bound evidence over size quotas or repeated
unchanged checks. Edit prose for clarity without stylistic bans or style receipts.

## Consequences

Entrypoints become shorter and presets retain their purpose during recovery.
Direct fallback provides less independent scrutiny, explicitly reported. Evidence
reuse requires trustworthy candidate/input provenance; uncertainty forces a rerun.
Prompt contracts and CLI-sequence tests cover structure and executable recovery,
but do not establish that a model follows the instructions; live agent evaluation
remains a separate check.
