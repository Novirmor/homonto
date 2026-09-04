# Configure one workflow root

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

`onto` and `to` currently hard-code their records beneath `docs/`, although a
repository may need one designated directory for generated workflow material.
The two frameworks are mutually exclusive, so separate roots add configuration
without enabling a valid combined workflow.

## Decision

We will add `[workflow] root = "<relative path>"` to `homonto.toml`. Omission
means `docs`, preserving current layouts. Both frameworks resolve their state,
archives, specifications, guides, locks, Git bookkeeping paths, and skill
instructions through this shared root. The path stays beneath the configuration
repository.

Changing the root while any workflow state exists fails closed. Homonto will not
move active workspaces, archives, receipts, or locks automatically; a dedicated
migration can make that move explicit later.

## Consequences

New repositories can keep workflow material outside the default `docs/` tree
without separate onto/to settings. Existing configurations remain unchanged.
Root changes require manual cleanup or a future migration command, which avoids
silently splitting durable workflow state across directories.
