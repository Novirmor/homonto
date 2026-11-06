# Run swarm candidates as stateless workspace tasks

- **Status:** Accepted
- **Date:** 2026-11-06

## Context

M6 asks for two or three implementations of one task from the same base,
each in its own worktree, with fixed models and a shared brief. Workspace
tasks (ADR 0068) already allocate, verify and release one isolated checkout
per repository. Native lifecycle engines (onto, to) bind exactly one
execution checkout per change and repository, so they cannot hold parallel
candidates of one change. Routes reach agents only through their projected
files (ADR 0099): OpenCode's task tool takes no per-call model, so a
"candidate model" cannot be a runtime argument.

## Decision

- A swarm is a record under `.homonto/swarms/<name>/` over one workspace task
  per candidate, named `<swarm>--<candidate>`. Candidate names are lowercase
  letters and digits, so no two pairs share a task, branch or worktree.
- Every candidate uses worktree isolation. Creation records the base of the
  first candidate and refuses any later candidate whose base differs.
- Only a stateless engine that supports start, resume, integration-check and
  release can run a swarm. Onto and to are refused.
- A swarm has two or three candidates. More is a different experiment.
- A candidate is an installed agent. Its fixed model is the model that agent
  is routed to on each projected harness, recorded at creation. Different
  models come from different agents (a pin or a BYO subagent), not from a
  runtime override.
- The brief is copied into the record with its digest. Resuming with another
  brief, engine, repository set or candidate list is refused.
- The parent session dispatches the candidates; the binary launches no harness.

We rejected having the binary start headless sessions (`claude -p`,
`opencode run`) per candidate. It would enforce the model directly, but it
bypasses projected permissions and agent prompts, needs per-harness auth and
permission flags, and turns homonto into a scheduler.

## Consequences

Allocation, checks and cleanup are the workspace-task code paths, unchanged.
Comparing models needs one agent per model, which is a config step for the
user. The model is fixed by projection, so it is only as current as the last
apply; the record shows what was routed at creation. Onto and to users cannot
swarm until a lifecycle supports several execution bindings per change.
