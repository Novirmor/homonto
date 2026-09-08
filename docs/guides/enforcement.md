# Workflow gates and observers

The workflow gates are hard **when the binary is invoked**, but nothing
forces an inattentive agent to invoke it. A tool hook can run diagnostics on
an event and surface a workflow-integrity problem. Observation or logging alone
does not prevent skipped commands, hand edits, or session completion.

The read-only diagnostic primitive is:

```
onto doctor --quiet
```

`onto doctor` is read-only; `--quiet` prints nothing
and signals health **only through its exit code**. It is non-zero when there
are findings: a missing docs directory, a missing or malformed change state,
a phase whose required artifact is absent, an unresolved dependency, an active
change marked archived, at least 3 failed verify rounds while the current result
is still `fail`, `tasks.md` / `plan.md` drift,
a malformed archive entry, or version skew between the binary and the homonto
that installed the framework. Historical failures remain recorded after a pass
but are not that unresolved-rounds finding. Structured evidence staleness checks
use the latest claim per unambiguous `(repo, task, scenario)`; superseded attempts
remain audit history. Duplicate `Scenario-ID` declarations are findings with
paths and line numbers even without a sidecar; ambiguous IDs provide no coverage
or trace supersession. No-spec presets declare each ID once in either tasks or
verification, using plain references elsewhere. See
[ADR 0053](../adr/0053-keep-history-without-blocking-fresh-verification.md).
A hook can act on the exit code.

In a repository using the `to` framework instead, `to doctor --quiet` has
the same read-only, exit-code-only interface. Both resolve the configured
records root and source scope where needed; they are not universally
config-independent. Use `--dir <config-root>`, even from a source worktree.
The example below works with the command swapped.

## Via the bundled OpenCode bridge

The shipped `onto`, `to`, and `h` frameworks project the read-only
`homonto-workflow` plugin by default. It observes the workflow snapshot when a
session goes idle, after debounced file-watcher updates, and during compaction.
It resolves the config from its materialized catalog and binding metadata, not
the session launch directory. It displays OpenCode toasts for phase, task, and
lifecycle changes, and for malformed or unreadable workflow state. A disappearing
record warns that completion is not established; an archive pending integration
is not completed work.

Refreshes share one in-flight read, with a trailing refresh for triggers during
that read. Transient subprocess/config/output failures produce an observation
error without replacing the last successful comparison with an empty snapshot;
a later success clears the error. Compaction awaits fresh status and includes
pending work and findings, or an explicit observation error, not stale success.
Shutdown cancels scheduled/in-flight work. Disable the bridge with:

```toml
[integrations.opencode]
workflow_bridge = false
```

The bridge is an observer, not an enforcement hook: it does not execute
`doctor`, block session completion, advance workflow state, or record evidence.
Use `onto doctor` or `to doctor` without `--quiet` for the full diagnosis;
use `--quiet` when only the exit code is needed.

## Custom OpenCode hook

OpenCode has no declarative command hooks; hooks live in a plugin. This optional
diagnostic example only logs a failure. Install and review it yourself, and run
the session from the config root or replace `directory` with that explicit root:

```ts
import type { Plugin } from "@opencode-ai/plugin"

// Runs `onto doctor --quiet` when a session goes idle; a non-zero exit means the
// onto workspace has an integrity finding. Read-only — it never mutates state.
// Logging is diagnostic; it does not block session completion.
export const OntoGuard: Plugin = async ({ $, directory }) => ({
  event: async ({ event }) => {
    if (event.type === "session.idle") {
      const result = await $`onto doctor --quiet`.cwd(directory).nothrow()
      if (result.exitCode !== 0) {
        console.error(`onto doctor failed (exit ${result.exitCode}): onto workspace integrity finding`)
      }
    }
  },
})
```

The example uses `session.idle`. Dropping `.nothrow()` makes a non-zero exit
throw from the handler, but throwing is not a documented guarantee that OpenCode
blocks session completion. A blocking policy needs a separately reviewed,
host-supported interception point; do not infer enforcement from a toast,
console message, or failed event handler.

## What this buys you

The binary owns transition gates; observers make progress, pending decisions,
and diagnostic failures visible. Pair `onto gate <change> --json` with doctor
for actionable diagnosis. Neither the bundled bridge nor the logging example
makes the workflow non-skippable at the tool boundary.
