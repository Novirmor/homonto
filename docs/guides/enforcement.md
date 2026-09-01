# Enforcing the workflow with hooks

The workflow gates are hard **when the binary is invoked**, but nothing
forces an inattentive agent to invoke it. A tool **hook** closes that gap:
it runs a check on an event (for example, when the agent stops) and fails
loudly on a workflow-integrity problem, so a skipped gate or a broken
workspace cannot pass silently.

The enforcement primitive is:

```
onto doctor --quiet
```

`onto doctor` is read-only and config-independent; `--quiet` prints nothing
and signals health **only through its exit code**. It is non-zero when there
are findings: a missing docs directory, a missing or malformed change state,
a phase whose required artifact is absent, an unresolved dependency, an active
change marked archived, ≥3 failed verify rounds, `tasks.md` ↔ `plan.md` drift,
a malformed archive entry, or version skew between the binary and the homonto
that installed the framework. That exit code is what a hook acts on.

In a repository using the `to` framework instead, `to doctor --quiet` has
the identical contract (read-only, config-independent, exit-code-only).
Every recipe below works with the command swapped.

## Via an OpenCode plugin

OpenCode has no declarative command hooks; hooks live in a plugin. Drop this
minimal plugin at `.opencode/plugins/onto-guard.ts` (or your global
`~/.config/opencode/plugins/`) and reference it in your `plugin` array
(`[plugins.opencode.onto-guard] source = "./.opencode/plugins/onto-guard.ts"`):

```ts
import type { Plugin } from "@opencode-ai/plugin"

// Runs `onto doctor --quiet` when a session goes idle; a non-zero exit means the
// onto workspace has an integrity finding. Read-only — it never mutates state.
// The exit code is surfaced to the session log so a violated gate cannot end a
// session silently.
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

Adjust the event (`session.idle`, `session.completed`) to taste. The guard
above logs the failure through `console.error`; if you would rather abort
the event handler, drop `.nothrow()` and let the non-zero throw. The plugin
is a code artifact rather than declarative config, so install and review it
yourself — homonto does not project or test the plugin's execution.

## What this buys you

The binary already owns the gates; the hook makes them **non-skippable at
the tool boundary**. Pair it with `onto gate --json` (structured decisions)
and the doctor findings (version skew, verify rounds) for a workflow that
surfaces its own violations rather than trusting the agent to.
