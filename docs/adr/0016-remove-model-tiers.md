# Declare subagent models explicitly; drop model tiers

- **Status:** Superseded by 0023
- **Date:** 2026-07-21
- **Change:** remove-model-tiers

## Context

Model selection ran through a tier indirection. Each catalog subagent declared
`role: architectural|coding|review|trivial` in its `homonto` frontmatter; the
role selected a `[models.<tool>.<tier>]` block in `homonto.toml`; `agentfm`
merged that with any `[subagents.<name>.<tool>]` override. Config validation
required every enabled tool to declare *all* tier names known to the code, and
the adapters read the tier map directly for the **main** session model
(`[models.claude.architectural]`; OpenCode `model` / `small_model` from
`architectural` / `trivial`).

The indirection coupled config validity to code internals: adding the `review`
tier in code made `homonto doctor` fail on the shipped config, for a reason the
config could not see. The failure was also anonymous — `models.claude.review is
required` never said which agent needed it. The override mechanism was already
a complete way to say the same thing, explicitly and per agent.

## Decision

We will delete the tier system and require every declared subagent to name its
own model per tool.

- `role:` frontmatter, `[models.<tool>.<tier>]` config blocks, and the
  `TierNames`/`Tiers`/`Roles` resolution in `agentfm` are removed. A legacy
  `[models.*.*]` table is rejected at load, naming the offending block — there
  is no shim and no silent migration.
- `[subagents.<name>.<tool>]` in `homonto.toml` is the single source of subagent
  models. Framework declarations still own agent *existence*; the installer
  config owns model choice.
- Every declared subagent must resolve a non-empty model for each tool it is
  projected to, or config load fails with
  `subagents.<name>.<tool> model is required`. The renderer enforces the same
  invariant for production renders as a backstop.
- Homonto no longer manages the main session model. The tool's own default
  applies unless the operator sets `[settings.claude].model` /
  `[settings.opencode].model` / `.small_model` explicitly.

## Consequences

- A config can no longer be invalidated by a code-side tier rename: the only
  names in play are agent names the config already spells out.
- Errors name the offending agent and tool, so the fix is mechanical.
- `homonto.toml` is more verbose — one block per agent per targeted tool — which
  is the accepted cost of centralization. The scaffold template ships the shape.
- Breaking for existing configs. Operators delete their `[models.*.*]` blocks,
  add a `[subagents.<name>.<tool>]` block per agent, and move any pinned main
  model to `[settings.<tool>].model`. Rollback is reverting the change; there is
  no in-place downgrade.
- Adding an agent to a framework now requires every installer to declare its
  model. This surfaces as a named load-time error rather than a silent
  model-less render.
