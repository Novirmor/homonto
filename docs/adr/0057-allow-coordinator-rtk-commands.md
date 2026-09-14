# Allow coordinator RTK commands

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The coordinator's trusted shell baseline already allowed unmatched RTK requests,
but no explicit rule described that authority when the RTK proxy was configured.
Agents therefore used indirect scripts while trying to infer whether RTK commands
were permitted.

## Decision

When the RTK proxy is configured, the coordinator will render an explicit `rtk *`
allow. Later protected asks continue to require confirmation for recognized
publication, direct workflow bypass, and destructive non-Git operations.

## Consequences

The coordinator can invoke RTK directly for autonomous work without an allowlist
expansion. RTK remains capable of arbitrary execution, and known protected operations
still prompt instead of being hidden behind the wrapper.
