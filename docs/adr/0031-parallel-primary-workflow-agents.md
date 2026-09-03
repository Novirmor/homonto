# Give each workflow its own primary agent

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

The onto workflow has an OpenCode primary agent, while to exposes its
dispatcher only through commands and skills. Users cannot choose the lightweight
workflow as an agent entry point. A separate read-only homonto primary would
hide both action workflows behind another selection step.

## Decision

Keep `onto` as the primary agent and its `/onto` command route unchanged. Add a
parallel `to` primary agent with the same orchestration capabilities and route
`/to` into it. Both framework catalogs install the `homonto` knowledge skill.
The skill explains projection and workflow selection, but primary agents own
workflow actions.

## Consequences

- A user selects `onto` for the evidence-gated workflow or `to` for the focused
  workflow.
- Each framework requires a model route for its own primary and four specialist
  agents.
- The shared knowledge remains one skill instead of duplicating a third agent
  profile and permission boundary.
