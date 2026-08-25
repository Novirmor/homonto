# A repair round supersedes the attempt it repairs

- **Status:** Accepted
- **Date:** 2026-08-25

## Context

A failing check issues a repair round for the same work units as the round
that failed, and the workflow then integrates again. Two mechanisms
disagreed about what "again" means.

Material selection was by generation, and a repair carries the same
generation as the round it repairs — the generation only closes once the
repair finishes. So selection returned the failed original alongside its
repair.

An integration area is named for its work and member, so the second round
found the first round's area still holding the first round's materials.
Cherry-picking on top of that either stopped with "the previous cherry-pick
is now empty" — which is how this surfaced, as a workflow that could not
get past a failed check — or succeeded and left the failed attempt on the
integration branch while the record said the checks passed.

## Decision

**Material is selected per work unit, not per generation.** For each unit,
the newest finished implementer assignment wins, and a repair beats an
implement outright: a repair is by definition issued after the attempt it
replaces.

**An integration round starts from the base.** Re-entering an existing
integration area resets it to the shared base commit before applying this
round's materials. A DIRTY area is refused instead of reset.

## Consequences

The integration branch after a repair contains the repair and not the
attempt, which is what the archived record claims.

Refusing a dirty integration area means a round cannot start over
someone's unfinished conflict resolution. Discarding that silently is not
a decision the code gets to make; the person who left it there resolves or
abandons it.

Rolling back a re-entered operation no longer removes the integration
worktree an earlier round created — the create effect records at Prepare
whether the worktree was already there. Before, a failed second round tore
down the first round's area and reported the teardown's failure instead of
the reason it rolled back.
