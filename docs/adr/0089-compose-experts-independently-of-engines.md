# Compose experts independently of engines

- **Status:** Accepted
- **Date:** 2026-11-09
- **Partially supersedes:** [0067](0067-separate-workflows-from-engines.md)
  (package kinds and no's exclusive worker selection),
  [0085](0085-bring-your-own-mcp-servers-and-skills.md)
  (discovery of user-provided agents and skills)

## Context

Engines govern implementation but do not supply architecture, UI/UX, database
or security methods. Separate specialist workspaces contain that knowledge but
cannot compose it with implementation. A fixed coordinator spawn list also
cannot discover BYO specialists.

## Decision

Add `[experts.*]` and package `kind = "expert"`. Builtin, local and pinned remote
packages share an explicit skill/analyst/reviewer contract. Managed analysts and
reviewers are read-only, shell-free, non-delegating advisors; the parent writes
requested artifacts and the selected engine's writer implements application
changes using the domain skill and acceptance criteria. Expert packages are
standalone and carry no plugins or package dependencies. Ship architecture,
UI/UX, database and security first.

`external:<agent-id>` references an existing native agent, with an optional skill
and reviewer. Its files, model and permissions remain user-owned; availability
is checked by the coordinator at invocation, not guessed by the projector.
MCP servers remain bring-your-own and are not configured by this feature.

Generate the installed expert inventory and coordinator dispatch list from the
configuration. An expert-only installation includes the shared coordinator and
`/expert` without requiring an engine, Git repository or lifecycle records.
No keeps its two execution workers; experts may advise and add domain review.

## Consequences

Domain knowledge is reusable across standalone analysis and every engine.
Installation, re-rendering and removal reuse package projection and ownership.
Custom managed experts must adopt the advisory contract; other native agents
use explicit external references and retain their own permissions. Projection
and prompt tests establish installation/contracts, not expert reasoning quality
or visual correctness. Rendered UI inspection and live model evaluation remain
separate evidence.
