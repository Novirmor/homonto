# Workflows

Homonto runs one workflow per workspace. Task is the short path for bounded
work. Change records the decisions needed for work with a durable design or
acceptance record.

## Choose A Workflow

| Workflow | Path | Use it for |
|---|---|---|
| Task | `plan -> do -> done` | Work that needs scoped implementation, verification, and an archived task record. |
| Change Full | `open -> design -> build -> verify -> close` | New capability, public API or schema work, cross-module work, or durable design decisions. |
| Change Fix | `open -> build -> verify -> close` | An existing behavior defect with a reproducible failure. |
| Change Tweak | `open -> build -> verify -> close` | A bounded behavior, configuration, documentation, or prompt change. |

The workspace workflow controls which top-level commands and host entry point
are available. Start with Task when a checked-off record is enough; start a
Change when humans must approve scope or design before implementation.

## Task

Task begins with exploration, a human-authored goal and checklist, and a
skeptic's review. Implementers work in isolated areas, an integration
assignment combines their output, and Homonto runs the configured checks. A
reviewer and skeptic assess the integrated result before Homonto archives the
record.

Task has no separate plan-approval gate. Questions that affect scope, safety,
or acceptance still require a human decision.

Failed checks or blocking findings start a repair round. Repair output returns
to integration before checks run again. After three failed repair rounds,
Homonto asks whether to continue, accept the blocking findings as deviations,
or abandon the Task. A failing check remains failing after a finding is
accepted.

## Change

`change start` first creates a local classification candidate. Explorers and a
skeptic inspect the request, then a human selects Fix, Tweak, or Full. Until
that confirmation, no portable Change record or Change documents exist. The
local runtime database still records the candidate and its abandoned state.

Full writes and approves `proposal.md`, then writes and approves `design.md`
and `tasks.md`, before creating a build plan. Fix records the reproduction,
expected and actual behavior, and root cause. Tweak records the intended change
and its exact behavior delta.

Fix and Tweak pause when scope indicates a new capability, public API or schema
change, cross-module coordination, deep architecture change, material intent
expansion, work that should split, or more than five changed non-test files.
The file count is a warning. A human can continue with the broader scope or
upgrade to Full.

## Roles And Decisions

Every workflow uses four roles:

- Explorer: gathers facts without writing.
- Implementer: changes only its issued isolation area and scope.
- Reviewer: assesses the integrated result.
- Skeptic: challenges assumptions before implementation and evidence after it.

Humans choose the workflow, confirm Change classification, approve Full scope
and design, answer consequential questions, and resolve blocking findings.
Homonto owns state transitions, verification execution, document regions it
generates, and final archival.

## Freshness And Invalidation

Before issuing a next action, Homonto compares recorded fingerprints with the
current workspace. A changed input invalidates the evidence that depended on
it. Examples:

| Changed input | Task returns to | Full Change returns to | Fix or Tweak returns to |
|---|---|---|---|
| Member list | exploration | Open exploration | preset exploration |
| Path classes | exploration | Open exploration | preset scope assessment |
| Verification configuration | checks | verification checks | preset checks |
| Integrated source | checks | verification checks | preset checks |

Document changes return to the document's owning phase. A changed Full
`tasks.md` returns to Design; changed Fix, Tweak, or preset tasks return to the
preset's classification and scope path. Fresh actions replace invalidated ones,
so a host must request `next --json` after a return.

## Completion And Portability

Completion archives Task or Change records and leaves integration branches or
staged directories for external repository policy. Homonto does not merge them.

`handoff` creates a portable checkpoint for active work. `attach` rebuilds local
state on another machine and resumes from the start of the checkpointed phase.
See [Recover or transfer work](../how-to/recover-or-transfer-work.md) for the
operator procedure.
