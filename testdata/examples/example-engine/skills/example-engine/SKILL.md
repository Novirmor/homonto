---
name: example-engine
description: Example engine entry skill — implement a change, then have example-reviewer review it.
---

# example-engine

1. Read `homonto explain settings example-engine --json` for `review`.
2. Implement the requested change in the assigned checkout.
3. When `review` is `always`, dispatch `example-reviewer` with the diff and
   report its findings with the result.
