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

For the `to` lifecycle workflow, `to doctor --quiet` has
the same read-only, exit-code-only interface. Both resolve the configured
records root and source scope where needed; they are not universally
config-independent. Use `--dir <config-root>`, even from a source worktree.
The example below works with the command swapped.

## Via the bundled OpenCode bridge

The shipped `onto`/`to` lifecycle frameworks and `h` GitHub skill bundle project
the `homonto-workflow` plugin by default. Its observer reads the workflow snapshot when a
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
Compaction and resumed model requests also receive bounded recovery excerpts
for up to three nonterminal generations, including task context, decisions,
artifact pointers, and validated source directories. The overall context budget
is 16 KiB with a 1.5-second wait; truncation and unavailable reads are explicit.
Multiple changes do not automatically select an owner. The coordinator can use
`homonto_status` and `homonto_handoff` for read-only structured inspection.
Shutdown cancels scheduled/in-flight work. Disable the bridge with:

```toml
[integrations.opencode]
workflow_bridge = false
```

The observation and recovery hooks are not enforcement hooks: they do not execute
`doctor`, block session completion, advance workflow state, or record evidence.
Use `onto doctor` or `to doctor` without `--quiet` for the full diagnosis;
use `--quiet` when only the exit code is needed.

With the builtin `h` bundle configured, the plugin separately exposes
coordinator-only `homonto_github_draft`, `homonto_github_status`, and
`homonto_github_publish` tools. These stage exact issue/PR comments and supported
formal reviews, then consume matching native question replies before publishing.
Permission replies alone never approve a draft. Event hooks, compaction, and
recovery never publish. Drafts are session-bound, expire after 15 minutes, and
are lost on restart; approval is never reconstructed from conversation summaries.
Uncertain sends are reconciled rather than resent. This is a workflow approval
mechanism, not isolation from trusted shell or host API clients.

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
