---
name: onto
description: Dispatch an explicitly selected onto workflow or resume an existing onto change. Discover state and route full, fix, or tweak phases. Unselected new work goes through homonto workflow selection; informational questions do not start a change.
---

# onto — Workflow Dispatcher

## Purpose and entry

Route a selected onto change through open → design → build → verify → close.
Fix and tweak own their preset lifecycles. The dispatcher performs no phase work.
Use it for an explicit onto choice or a matching existing onto change. Answer
informational questions without creating workflow state.

Follow [autonomy](../homonto/references/autonomy.md), including OpenCode's built-in
`question` tool for required decisions. Continue through the full success endpoint:
verification, archival, integration and authorized publication. An explicit earlier
endpoint, pause, blocking decision or hard blocker can stop the invocation.

## Required inputs

- Request and any explicit path/endpoint or recorded decisions.
- Generated `references/workspace.md` for exact config, records and source roots;
  inspect the active configuration if absent or stale.
- Shared [workspace policy](../homonto/references/workspace-policy.md) before writes.
  Keep all workflow calls that accept it on `--dir "<configRoot>"`.
- `onto` binary and installed framework; only the binary writes state/sidecars.

## Ordered actions

### 1. Preflight

Run `onto version` and check the framework-install gate at configRoot. For missing
or incompatible tooling, follow [bootstrap recovery](../homonto/references/autonomy.md#root-and-bootstrap).
No handwritten state or fabricated installation directories. Required failures
block mutations; authorized investigation can continue.

Read generated `references/tooling.md` and run declared provider probes; optional
provider failures warn and proceed. Use `references/tmp.md` when present for
transient evidence. Check actual dispatch using
[execution policy](../homonto/references/execution.md) before creation.

### 2. Discover and select

Inspect both workflow inventories before new work. Read
[discovery](references/discovery.md) for active/archive matching, dependencies,
dirty-work attribution and first-use bootstrap. Resume a unique matching change;
ask through `question` only for unresolved identity or scope conflicts.

For genuinely new work, apply [workflow selection](../homonto/references/workflow-selection.md).
`/onto` selects the onto family, not fix/tweak/full: recommend viable paths and
ask once unless a path was specified. `/onto-open`, `/onto-fix` and `/onto-tweak`
select explicit paths. Never reopen selection on resume or silently downgrade
full onto to a preset. Selection and escalation use that reference's eligibility
table; verification risk is assessed separately by `onto-verify`.

### 3. Derive

Read `onto state <name> --json --dir "<configRoot>"` and the referenced artifacts.
Use its `derived_phase` and `phase_mismatch`, not conversation history. On resume,
read `onto handoff <name>` and any truncated source artifacts before acting.

Files win downward; gates win upward. For mismatches, missing state, preset
upgrades or a defect after a passing report, follow
[recovery](references/recovery.md) before routing. The routed skill accepts the derived phase
and skips `onto advance` when recorded phase is already ahead.
Only `onto set workflow <name> full` records an objective preset upgrade; never
hand-edit state or use a backward phase write.

### 4. Route

| Selected workflow / derived state | Load |
|---|---|
| fix, any phase | `onto-fix`; its resume map selects setup/build/verify/close |
| tweak, any phase | `onto-tweak`; its resume map selects setup/build/verify/close |
| full, open | `onto-open` |
| full, design | `onto-design` |
| full, build | `onto-build` |
| full, verify | `onto-verify` |
| full, close | `onto-close` |
| done | Report archived and integrated; new work requires its own selection |

Respect a preset's recorded open/design setup even if empty scaffolds derive
build. Phase completion loads the next phase in this invocation; implementation
checkoffs alone are not completion. GitHub intake carries its authorized delivery
target and integration mode into the workflow before close; publication still
uses the [shared contract](../homonto/references/publication.md).

## Delegation

| Task | Worker |
|---|---|
| Locate behavior or investigate a bounded question | `onto-explorer` |
| Implement an assigned source task | `onto-implementer` |
| Review a candidate diff | `onto-reviewer` |
| Challenge final evidence | `onto-skeptic`, lenses from `onto-verify` |

The coordinator owns planning, scope, decisions, workflow calls, records and
commit validation. Workers never prompt the user. Supply Owner, Repo, absolute
Cwd and complete readable evidence. Follow the shared execution policy for
unavailable dispatch; never treat a denial or blocked worker as absence.

Schema 2 same-repo writers stay serial in their validated bindings. Legacy schema
0/1 combined workflows may use disjoint task worktrees only under
[subagent-protocol.md](../onto-build/references/subagent-protocol.md)'s five
conditions. Read-only questions may run concurrently with bounded, distinct
assignments. In direct mode the coordinator edits; in subagent mode workers edit
only assigned source tasks and may commit only with explicit task authorization.

## Completion evidence and next route

Run `onto gate <name> --json` to discover missing evidence tokens. Perform the
review, then record its result; a token does not imply personal user approval.
Select technical defaults from evidence under autonomy. Never invent evidence,
accept deviations, waive obligations or abandon a change without required intent.

Phase skills own their exit checklists and the next route. Edit retained prose
for clarity using `onto-no-slop`; no separate style receipt is required. Preserve
machine-read markers, task identifiers, normative requirements and literal output.
At the final endpoint confirm `done` derives from completed integration, not
merely an archived directory. Report blockers with evidence and preserved state.
