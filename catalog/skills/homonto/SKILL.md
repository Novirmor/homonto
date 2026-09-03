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
skill and owns its workflow mutations, user gates, commits, and delegation.

When answering a question about an installed repository, inspect its declared
framework and projection state before claiming what is active. Keep the answer
direct and name the command or agent the user needs next.
