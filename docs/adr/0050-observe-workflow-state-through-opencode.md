# Observe workflow state through OpenCode

- **Status:** Accepted
- **Date:** 2026-09-07

## Context

Workflow progress is stored in local files, so OpenCode has no indication when
a task, phase, or workflow-health condition changes. Asking a plugin to manage
those files would duplicate the workflow state machine and let UI code bypass
its evidence and confirmation gates.

## Decision

We will project a homonto-owned, project-local OpenCode plugin by default for
the shipped `onto`, `to`, and `h` frameworks. It reads a stable `homonto
workflow snapshot --json` command, keeps comparisons only in memory, and
reports transitions and observer-safe findings through OpenCode's TUI. Projects
may opt out with `integrations.opencode.workflow_bridge = false`.

The plugin resolves the selected config from its materialized catalog and
binding metadata, not the session launch directory. Idle, debounced file-watcher,
and compaction triggers share one in-flight read and request a trailing refresh
when needed. Comparison uses durable workflow identity and explicit lifecycle
status, including archives pending integration; disappearance is not completion.

Transient observation failures are separate errors, not empty snapshots: retain
the last successful comparison, report the error, and clear it after a successful
refresh. Compaction waits for fresh status and includes pending work/findings or
the observation error rather than stale success. Disposal cancels outstanding
work. This is observation only: the bridge does not run doctor, block session
completion, mutate workflow state, or certify verification or remote publication.

## Consequences

- The runtime receives current workflow context without a second writer of
  workflow state or a persistent event log.
- The bridge adds an OpenCode plugin and depends on its event/TUI API; a
  headless or incompatible client loses notifications but not workflow safety.
- Detailed diagnosis and every workflow mutation remain explicit `onto` or
  `to` commands. A custom blocking policy still needs a separately reviewed
  plugin.
