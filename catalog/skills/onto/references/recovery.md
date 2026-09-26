# Onto routing and repair recovery

Read on mismatch, compaction, interrupted close, preset upgrade, or a defect
found after a passing report. Binary state remains authoritative for recorded
decisions; artifact evidence determines what work remains.

## Derive and reconcile

Read the binary's `derived_phase` and `phase_mismatch` from `onto state --json`.
Read `references/state-yaml.md` for schema and evidence meanings. If an older
binary lacks derivation, use this strongest-evidence-first table:

| Evidence | Working phase |
|---|---|
| Archived, integration pending/invalid | close |
| Archived, integration complete (or legacy archive without sidecar) | done |
| `design.md` has `Status: Under revision` | design |
| `verification.md` has canonical passing `Result:` | close |
| At least one task and all checked | verify |
| Confirmed design, or preset with complete setup | build |
| Full change, draft design or unchecked tasks without confirmed design | design |
| Full change, proposal only | recorded open/design |
| Missing proposal / incomplete workspace | open |

Do not infer a completed preset setup from empty scaffolds. Recorded open/design
or missing proposal review/isolation/contracts routes to preset step 1; repair
only missing items and never repeat `new` or reset existing decisions.

- **Derived earlier:** note the mismatch in `notes.md`, route to that phase and
  preserve the later recorded phase. The skill accepts the derived phase, repairs
  artifacts and skips `onto advance` while state is ahead. Redispatch until caught up.
- **Derived later:** perform the recorded phase's remaining exit review and record
  the required tokens before advancing. Prepared files alone do not record a decision.
- **Verify → close:** a current passing report beside recorded verify is a lagging
  phase write, provided the pass still describes the candidate and required evidence
  is recorded. Run `onto advance`, then close; do not discard valid evidence merely
  to repeat a phase. Missing/stale evidence must be repaired first.
- **Open ↔ design:** a full proposal alone cannot distinguish these phases. Trust
  recorded open/design until its next deliverables exist; missing proposal means open.

Use `onto handoff <name>` for content recovery; read full artifacts if truncated.
Never derive decisions or permissions from conversation memory. If references are
missing, report the gap; read-only investigation can proceed, but do not invent
required grammar, tokens or evidence to clear a gate.

## Workflow identity and upgrade

Use proposal `Preset:` first (an upgrade annotation means full), then confirmed
or under-revision design (full), then a legacy `fix/` or `tweak/` branch prefix,
otherwise full. A branch prefix cannot downgrade a designed change. Preserve an
explicit full choice and surface mismatches. Objective preset escalation uses
the shared [eligibility table](../../homonto/references/workflow-selection.md#eligibility-and-escalation).

Record an upgrade with `onto set workflow <name> full`, annotate
`Preset: fix|tweak (upgraded to full YYYY-MM-DD)`, expand the reduced proposal,
and create `design.md` marked `Status: Under revision`. Invalidate any old passing
report and set verify-result pending before design is reconfirmed. Backfill design
without moving the recorded phase backward. Continue automatically unless scope
now requires a user-owned decision. Never downgrade full to a preset automatically.

## Active repairs versus new work

A confirmed defect before archive, within the existing acceptance criteria, is
an autonomous repair even after verification passed. No new consent is needed.
First invalidate `verification.md` to `Result: superseded (repair <date>)`, run
`onto set verify-result <name> pending` (or retain this round's recorded fail),
and append the repair task. Keep the recorded phase and existing isolation.

| Workflow | Repair route |
|---|---|
| full | Paired tasks/plan contract; `onto-build` |
| fix | Inline task contract; `onto-fix` step 2, failing test first |
| tweak | Inline task contract; `onto-tweak` step 2 |

Return through verification before close. Preset repairs do not require a new
plan or design unless eligibility triggers escalation. Scope expansion needs a
decision through OpenCode's built-in `question` tool.

An archived pending change only finishes its recorded integration. A defect in
completed history is new work, referencing that archive and following normal
selection/intent rules. Never modify immutable archives to absorb a repair.
`onto abandon` always requires explicit user intent; it retains an unsuccessful
workspace in place, with no spec merge or successful archive move.

## Missing or malformed state

Report the recovery condition and infer routing only from available artifacts.
Never hand-write `onto-state.yaml`. Restore trusted Git history when possible;
if genuinely lost, obtain explicit destructive-recovery intent before quarantining
the orphan and creating a fresh change. `onto abandon` cannot load absent state.
Apply the boundary table in `state-yaml.md`; artifacts do not recreate lost consent.
