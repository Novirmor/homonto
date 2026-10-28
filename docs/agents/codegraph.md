# Grounding with CodeGraph

CodeGraph is this repository's one local code index. Use it to answer "where
is X", "who calls X", and "what does changing X touch" from the index instead
of from memory or a guess ([ADR 0066](../adr/0066-ground-development-in-codegraph.md)).

It is a developer tool, not a product dependency. What homonto *ships* as a
`code_intel` tooling provider for user repositories is a separate, configured
choice ([ADR 0022](../adr/0022-tooling-providers-are-declared-not-shipped.md)).

## Setup

Install the CLI (`npm install -g @colbymchenry/codegraph`), then from the repo
root:

```bash
task index            # first run: codegraph init (builds .codegraph/)
                      # later runs: codegraph sync (incremental)
codegraph install     # optional: register the MCP server with your agent
```

`.codegraph/` is per-machine and gitignored except for its own `.gitignore`.
Building or refreshing the index is the developer's call; an agent asks before
running `init` on a clone that has none.

## Using it

| Question | CLI | MCP tool |
|---|---|---|
| Where is a symbol? | `codegraph query <name>` | `codegraph_search` |
| How does an area work? | `codegraph explore <query>` | `codegraph_explore` |
| One symbol's source plus callers and callees | `codegraph node <name>` | `codegraph_node` |
| Who calls it / what does it call? | `codegraph callers <sym>` / `codegraph callees <sym>` | `codegraph_callers` / `codegraph_node` |
| What breaks if it changes? | `codegraph impact <sym>` | — |
| Which tests cover the files I changed? | `task affected` (`codegraph affected`) | — |

Prefer the index over grep for call-graph and impact questions. Grep is still
the right tool for literal strings, such as error messages, config keys, and
prose.

## Absence and staleness

Both are normal, and neither is a reason to stop.

- **No `.codegraph/`**: read the files directly and say so.
- **Index exists**: run `task index` (cheap, incremental) before relying on it
  after pulling or switching branches.
- **Index suspected stale and not refreshed**: treat it as absent. A
  confidently stale answer is worse than an admitted gap.

## Saying which grounding you used

When grounding affects a conclusion, name it, for example "from CodeGraph,
synced just now" or "no index, so I read the files directly". It costs one
clause and tells the reader how much to trust the claim. Never imply an index
confirmed something it did not.
