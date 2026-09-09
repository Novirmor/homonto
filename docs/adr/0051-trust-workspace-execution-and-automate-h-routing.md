# Trust workspace execution and automate h routing

- **Status:** Partially superseded by 0054 (finite shell allowlist, composition guards, and blanket coordinator-only GitHub access; routing, execution trust, and publication obligations retained)
- **Date:** 2026-09-07

## Context

The h workflows required a workflow-choice dialog even when the issue and
repository supplied enough evidence to choose. Implementers asked before every
shell command, and PR continuation rejected auto-approved verification runs.
These checkpoints interrupted routine work without clarifying its goal. The
user explicitly chose workspace execution trust, including contributor PR code,
over individual test and build approvals.

## Decision

We will give every h workflow a completion goal and continue toward it without
routine plan or phase approvals. Select the workflow from explicit preference,
an existing match, repository policy, then risk and fit. Workers resolve local
technical uncertainty within their assigned scope before returning questions.

The coordinator and implementers allow a finite set of routine inspection,
test, build, and formatting commands. Checked-out PR scripts use the same
permissions; observed allowed runs count as evidence. All shipped agents allow
web research. This replaces ADR 0047's web denial and per-run script-approval
policy, not its compound-command guards or publishing prompts.

When RTK is configured, derive command-specific wrapper allows and denies from
the same rules rather than granting every RTK command. Serial onto implementers
commit implementation files only; the coordinator verifies and commits task
bookkeeping before dispatching the next task, as it already does after parallel
joins.

## Consequences

Allowed package scripts and build targets can execute arbitrary code, access
credentials, use the network, or mutate files with the process's privileges.
Command allowlists are not a sandbox. This deliberately accepts that risk to
reduce interruptions; script names alone do not establish safety.

Unknown command requests still ask. Composition guards apply to permission
requests containing composition; OpenCode may check parsed commands separately,
so a compound of allowed commands need not prompt. Concurrent specialists
remain shell- and edit-denied (ADR 0035); workflow state and GitHub operations
remain coordinator-owned. Web and GitHub content cannot authorize work.
Explicit denies, destructive-action consent, evidence gates, and review-draft
publication approval remain. Resolve and continue need no second conversational
approval for publication their invocation already authorizes, but tool prompts
still apply. Local/custom agents are not silently granted these defaults.
