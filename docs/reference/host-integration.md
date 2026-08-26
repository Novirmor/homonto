# Host Integration Reference

Homonto supports Claude Code and OpenCode on Linux and macOS. `host install`
generates only the entry point for the workspace's selected workflow.

## Generated Files

| Host | Directory | Generated components |
|---|---|---|
| Claude Code | `.claude/` | Workflow skill, command, and managed resume-probe and write-guard hooks in `settings.json`. |
| OpenCode | `.opencode/` | Workflow skill, command, and plugin that normalizes the resume probe and write guard. |

The generated command and skill invoke the binary protocol. They do not carry
workflow transitions, document rules, routing policy, or subagent prompts.

## Ownership And Conflicts

Wholly managed generated files carry a Homonto content marker. On a later
install, Homonto updates a file only when its marker and content identify it as
managed. It refuses a missing, altered, or unowned managed file rather than
overwriting it.

`--adopt` explicitly replaces a conflicting file. A plan with any conflict is
refused as a whole, so its wrappers and hooks do not end up from different
versions.

Claude `settings.json` is shared configuration. Homonto rewrites only hook
entries that invoke Homonto and preserves unrelated settings, permissions,
models, and hooks. It refuses to rewrite invalid JSON. This shared document
does not use the wholly-managed file marker.

## Drift And Probes

`doctor` reports missing, changed, and conflicting integration files. It does
not repair them automatically.

The generated session probe uses `host probe`. An idle workspace adds no resume
context. One active work is reported as resumable. Ambiguous work is reported
as ambiguous, with no automatic choice.

For installation commands and `.gitignore` behavior, see [Install a host](../how-to/install-a-host.md).
