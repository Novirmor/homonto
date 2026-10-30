---
name: homonto
description: Shared homonto reference for configuration, catalog projection, workspace tasks, and selecting an engine; it does not dispatch work.
---

# homonto knowledge reference

Use this skill when you need to explain or reason about homonto configuration,
catalog projection, or the choice between onto, to and no engines. It is a
reference, not a workflow dispatcher and it does not authorize mutations.

Follow the [workspace and dirty-work policy](references/workspace-policy.md),
including for direct skill entry. The generated `references/workspace.md` names
the exact config, records, source, and worktree roots; inspect before writes.

## References

Engine and h skills link these shared policies; load the one the step needs.

| Reference | Read it when |
|---|---|
| [autonomy](references/autonomy.md) | Deciding whether to ask, what the trusted shell allows, bootstrap, uncertainty, preserving state |
| [workflow selection](references/workflow-selection.md) | Choosing `no`, `to`, `onto light`, or full onto for new work; light eligibility and escalation |
| [execution](references/execution.md) | Dispatching workers, parallel repositories, exploration, permission blocks, terminal cleanup, task and evidence rules |
| [workspace policy](references/workspace-policy.md) | Roots, dirty work, worktrees and receivers, source anchors, records checkpoints |
| [review triage](references/review-triage.md) | Deciding what a review or skeptic finding becomes, and whether a repair is proportionate |
| [debugging](references/debugging.md) | An unexpected failure during implementation or verification |
| [publication](references/publication.md) | Pushing, opening or updating PRs, and posting approved comments or reviews |

## homonto

homonto reads `homonto.toml` and projects declared resources into OpenCode. `homonto plan` previews changes; `homonto apply` reconciles them;
`homonto status`, `homonto doctor`, and `homonto explain` inspect state. Treat
`.homonto/` and projected `.opencode/` files as managed data. Change the source
configuration and re-apply instead of editing projections.

The catalog ships skills, commands, and agents with the binary. A `[workflows.*]`
or `[engines.*]` declaration installs its package. Each expanded agent needs
its own `[subagents.<name>.opencode]` model route.

## MCP servers and extra tools

`[mcps.<name>]` tables project MCP servers (command, env, scope) into OpenCode, and `[tooling]` declares shared providers such as a
shell proxy or code intelligence. When a user asks for one, edit
`homonto.toml`, then `homonto plan` (preview) and `homonto apply` (project) —
never edit the projected `opencode.jsonc` MCP blocks by hand; homonto manages
them and the next apply rewrites manual edits. `docs/guides/configuration.md`
documents every field.

`[repos]` names trusted sibling Git worktrees. For installed engines or h,
apply renders those resolved paths as OpenCode external-directory permissions
for builtin workers and the coordinator, with a deny baseline for other external
paths. Read-only specialists retain shell/edit denials. Custom agents receive no rule.
Declared repositories and their symlinks are trusted rather than sandboxed;
add a repo declaration rather than approving an arbitrary directory at runtime.

## Choose A Workflow

- **onto** serves work that needs a reviewable handoff and evidence-backed
  transitions: `open -> design -> build -> verify -> close`. Its `light` preset
  skips design and skeptics for bounded work.
- **to** serves focused solo work with less ceremony: `plan -> do -> done`.
- **no** provides an implementer and reviewer without a binary or lifecycle.
- **h** is an independently installed GitHub workflow bundle.

All engines may coexist at installation. Pick exactly one per task using
[new-change workflow selection](references/workflow-selection.md)
— an explicit command selects its path, while an unspecified new change gets
one recommended choice prompt. Preserve the selected engine on resume; do not
mix artifacts or convert the task into another engine. Follow shared
[execution policy](references/execution.md) for repository parallelism and
checkout-aware integration checks; no's shared metadata is not lifecycle state.

## Agent Entry Points

One coordinator agent serves both workflows: the `homonto` primary loads the
dispatcher skill for the change's workflow and owns its mutations, decisions,
commits, and delegation. `/onto`, `/to`, and every `/h-*` command route into
it.

When answering a question about an installed repository, inspect its declared
framework and projection state before claiming what is active. Keep the answer
direct and name the command or agent the user needs next.
