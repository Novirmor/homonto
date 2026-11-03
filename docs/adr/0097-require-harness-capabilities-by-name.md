# Require harness capabilities by name

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

ADR 0082 decided that an engine runs only on a harness that provides the
capabilities it needs, and that a gap is a plan error. Nothing declared either
side. `framework.toml` already had `[dependencies].capabilities`, but those
are semantic interfaces one package provides another (`name@major`), resolved
between packages, not features of the harness.

## Decision

- `internal/capability` holds the one vocabulary (`skills`, `commands`,
  `subagents`, `agent-tool-limits`, `shell-rules`, `question-dialog`,
  `context-hooks`, `permission-events`, `mcp`, `status-ui`). Manifest
  validation, plan and later `doctor` all use it.
- A package lists its needs in `[requires].harness`. The field is distinct
  from `[dependencies]` (other packages) and from `[requires.services]`
  (ADR 0094). An unknown name fails at load.
- An adapter declares what its harness provides at a pinned version, each
  capability `enforced` or `instruction-only`. Only `enforced` satisfies a
  requirement. OpenCode 2 declares everything enforced. An adapter without a
  declaration provides nothing.
- `plan` checks every declared package against each target adapter before
  projecting anything, and names the package, every gap and the harness
  version.

## Consequences

The capability tier of ADR 0082 is computable as soon as a second adapter
declares itself (#63). Requirements are honest only if packages keep them
current: a builtin that starts using a new harness feature must add it here,
or plan will let it run where it cannot work. On OpenCode nothing changes
today, since it provides every capability.
