---
name: example-triage
description: Example workflow process — read a tracker issue, pick a configured triage process, hand the work to an installed engine.
---

# example-triage

1. Read the issue through `tracker-cli issue view`.
2. Read the configured processes with
   `homonto explain settings example-workflow --json` and follow the named
   process's `steps`.
3. Ask the coordinator to select one installed engine for the change, with
   the requested endpoint. Never name an engine.
