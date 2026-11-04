# Let external engines own lifecycle records

- **Status:** Accepted
- **Date:** 2026-11-04

## Context

ADR 0093 made workspace tasks read an engine's contract instead of its name,
but a native-lifecycle engine still had to be builtin: the worktree registry
that binds an engine's source checkouts read only onto's `changes/` and to's
`tasks/` records (#67, #48). An external engine had no way to report its
lifecycle to the coordinator's task operations.

## Decision

An external native engine reports its lifecycle exactly as onto and to do:
through a record it owns in the workflow root and the worktree registry
homonto owns. The record lives at
`<workflow root>/engines/<engine>/<change>/<engine>-state.yaml` with a fixed
schema (`schema_version = 1`, `workflow`, `change`, a required stable `id`,
the engine's own `phase` names, `repos`, `repo_bases`, and a terminal marker).
homonto validates its shape, never its phase vocabulary. The registry, task
create/resume/check/cleanup and `homonto worktree` commands accept any
installed native engine whose name addresses such a record. No callback into
the engine's binary is needed: the record is the report.

## Consequences

An external engine can start, resume, integration-check and release workspace
tasks with no homonto source change. Its binary must write the record before
the coordinator creates the task, and must mark it terminal before release.
Engine names that cannot address a record (anything but lowercase letters,
digits and `-`) cannot own a native lifecycle. onto's and to's own record
formats are unchanged.
