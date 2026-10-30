# Drop the OpenCode 1 workflow plugin

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

OpenCode 2 is homonto's default target. The V1 `homonto-workflow` bridge
(`workflow_bridge = true`, or a `source = "homonto-workflow"` plugin
declaration) was kept as an opt-in. It wasn't OpenCode 2 compatible, conflicted
with `workflow_context`, and duplicated every plugin feature: the worker
permission handback (ADR 0077) had to be written and tested twice. The
permission observer carried an equivalent unused V1 entry.

## Decision

Remove both V1 entrypoints (`plugin.ts`) and their runtime tests. Bundled
plugins are now recognized by `index.ts`. `workflow_bridge = true` and an
enabled `homonto-workflow` plugin declaration fail at load, naming the key and
the replacement. A leftover `workflow_bridge = false` is ignored, because the old
docs told users to set it.

The engine keeps the V1 link destination as a permanently disabled entry, so
`homonto apply` still removes a stale owned `.opencode/plugins/homonto-workflow.ts`
link and still refuses to touch a foreign file there. The OpenCode adapter keeps
the old entry path only to match a recorded legacy plugin entry for pruning.

## Consequences

One plugin implementation per feature. OpenCode 1 users of the workflow bridge
must move to OpenCode 2 or stay on an older homonto. Generic V1 config
compatibility (settings, MCP entries, plugin arrays) is unchanged.
