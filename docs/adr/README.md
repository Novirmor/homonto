# Architecture decision records

Short records of decisions someone could later question. They exist to stop
the same argument being had twice.

Format and the test for whether one is owed: [`../agents/adr.md`](../agents/adr.md).

## Reading this directory

Numbers are four digits and never reused. A superseded record keeps its
file and its number; its Status becomes `Superseded by NNNN`. It is never
deleted or edited to match the new decision, because the record of having
changed course is the value.

**0023 is the pivot.** Homonto was a declarative projector for AI coding
tools — `homonto.toml` in, Claude Code and OpenCode configuration out —
with two workflow frameworks (`onto`, `to`) shipped alongside it. 0023
replaced all of that with a workflow orchestrator, and the code for the old
product has been removed.

Records describing the old product are marked superseded by 0023. They are
kept because "why did Homonto stop doing that" is a question worth being
able to answer, and because several of them — surgical merge, atomic writes,
referenced secrets — describe reasoning that outlived the product it was
written for.

Records about how this repository is DEVELOPED (0007, 0008, 0012, 0017) are
not superseded: they are still how the work is done.

## Current

| | |
|---|---|
| [0023](0023-rebuild-homonto-as-workflow-orchestrator.md) | Rebuild Homonto as a workflow orchestrator |
| [0024](0024-set-workspace-trust-defaults.md) | Set workspace trust defaults |
| [0025](0025-journal-crash-model.md) | Close journal crash windows with idempotent effects |
| [0026](0026-lease-recovery-commit-marker.md) | Journal lease recovery around an on-disk commit marker |
| [0027](0027-portable-handoff-commit-leak.md) | Document the portable-handoff commit leak and force-takeover semantics |
| [0028](0028-one-anchored-work-per-workspace.md) | One anchored work per workspace, resumed at the phase start |
| [0029](0029-repair-supersedes-the-attempt.md) | A repair round supersedes the attempt it repairs |

## Process

| | |
|---|---|
| [0007](0007-adversarial-multi-agent-verification.md) | Verify with adversarial fresh-context skeptic agents |
| [0008](0008-preflight-warns-not-halts.md) | Make the tooling preflight warn and proceed, never halt |
| [0012](0012-readopt-comet-openspec-workflow.md) | Re-adopt Comet + OpenSpec + Superpowers as the development workflow |
| [0017](0017-stop-committing-workflow-artifacts.md) | Stop committing workflow artifacts; the ADR is the record |

## Superseded by 0023

0001, 0002, 0003, 0004, 0005, 0006, 0009, 0010, 0011, 0013, 0014, 0015,
0016, 0018, 0019, 0020, 0021, 0022.
