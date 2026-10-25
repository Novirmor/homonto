# Coordinate parallel work by repository

- **Status:** Accepted
- **Date:** 2026-10-25

## Context

Multi-repository work has independent write scopes, but engine prompts still
serialize all implementers. Workspace allocation also requires lifecycle state,
excluding the catalog-only no engine. Per-repository tests cannot establish
that the resulting repository versions work together.

## Decision

All engines support model-directed parallel work across repositories, with one
repository per implementation assignment and one writer per checkout. Workers
may read every declared repository. Write scope is an assignment contract, not
a sandbox or a cross-session reservation. The user chooses isolation once per
task; resume reuses it. Independent work continues around blocked dependencies.

All three engines may be installed together. A workspace task selects exactly
one of onto, to, or no, and retains that engine on resume; other engines are
ignored for that task. Installation exclusions are removed.

Homonto stores lightweight workspace-task metadata: selected engine, checkout
identities, isolation choices, bases, and integration results. No retains only
its informational entry skill and two agent types, without lifecycle phases.
Onto/to retain their existing lifecycle and registered binding requirements.

Named `[integration.<name>]` checks specify `repos`, execution `repo`, relative
`cwd`, and argv `command`. Any changed participant selects the check. A JSON
context file supplies exact checkout paths and committed candidate revisions.
Unchanged inputs are pinned to their current clean revision; dirty inputs need
a user disposition. Participating writers pause during checks. Changed inputs
invalidate results. The model selects fallback checks only when none matches;
configured failures cannot be silently replaced.

The parent owns commits, integration, and publication. Parallel repository
reviews precede combined review. Partial delivery requires a user decision.

## Consequences

Parallelism needs no scheduler, planner agent, or mandatory plan files. Shared
workspace bookkeeping enables resume and cleanup independently of no's engine
content. Candidate commits precede integration checks. Read-only access expands
ADR 0039's repository grants while preserving ADR 0035's shell/edit denial for
reviewers. Coordination relies on the model; it does not prevent another
session from modifying a checkout during a run.
