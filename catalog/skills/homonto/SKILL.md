---
name: homonto
description: Shared homonto reference. Use when reasoning about homonto configuration, catalog projection, or choosing between the onto and to workflows; it explains managed paths and workflow boundaries but does not dispatch work.
---

# homonto knowledge reference

Use this skill when you need to explain or reason about homonto configuration,
catalog projection, or the choice between the onto and to workflows. It is a
reference, not a workflow dispatcher and it does not authorize mutations.

## homonto

homonto reads `homonto.toml` and projects declared resources into AI coding
tools. `homonto plan` previews changes; `homonto apply` reconciles them;
`homonto status`, `homonto doctor`, and `homonto explain` inspect state. Treat
`.homonto/` and projected `.opencode/` files as managed data. Change the source
configuration and re-apply instead of editing projections.

The catalog ships skills, commands, and agents with the binary. A framework
declaration installs its complete surface. Each framework-expanded agent needs
its own `[subagents.<name>.opencode]` model route.

## MCP servers and extra tools

`[mcps.<name>]` tables project MCP servers (command, env, scope) into the
tools that support them, and `[tooling]` declares shared providers such as a
shell proxy or code intelligence. When a user asks for one, edit
`homonto.toml`, then `homonto plan` (preview) and `homonto apply` (project) —
never edit the projected `opencode.jsonc` MCP blocks by hand; homonto manages
them and the next apply rewrites manual edits. `docs/guides/configuration.md`
documents every field.

`[repos]` names trusted sibling Git worktrees. For installed `onto` or `to`,
apply renders those resolved paths as OpenCode external-directory permissions
for the builtin writable primary and implementer, with a deny baseline for
other external paths. Read-only specialists and custom agents receive no rule.
Declared repositories and their symlinks are trusted rather than sandboxed;
add a repo declaration rather than approving an arbitrary directory at runtime.

## Choose A Workflow

- **onto** serves work that needs a reviewable handoff and evidence-backed
  transitions: `open -> design -> build -> verify -> close`.
- **to** serves focused solo work with less ceremony: `plan -> do -> done`.

One repository declares one workflow framework. Do not mix their workspaces or
move a change by hand. Use `to promote` when a to change must grow into an onto
change.

## Agent Entry Points

Selecting the `onto` primary agent starts the onto workflow. Selecting the `to`
primary agent starts the to workflow. Each primary agent loads its dispatcher
skill and owns its workflow mutations, decisions, commits, and delegation.

When answering a question about an installed repository, inspect its declared
framework and projection state before claiming what is active. Keep the answer
direct and name the command or agent the user needs next.
