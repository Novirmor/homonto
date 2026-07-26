# Apply write-scope parallelism across onto's phases; support parallel implementers

- **Status:** Accepted
- **Date:** 2026-07-27

## Context

[ADR 0019](0019-parallelism-follows-write-scope.md) set the rule that
concurrency follows what an agent writes. onto already practised parts of it —
two parallel skeptics in verify, concurrent explorers in design, per-task
review fan-out in build — but the guidance had drifted in three ways.

The dispatcher and `onto-build` both stated, without qualification, that "the
orchestrator owns every edit and commit; the subagents only read and report."
That is true under `execution: direct` and false under `execution: subagent`,
where `onto-implementer` is the one agent granted write capability. It sat
three lines from a table listing that agent as **edits**, in exactly the place
an agent decides whether to delegate.

Parallel implementers were documented only in `subagent-protocol.md`, behind
five conditions, while the two top-level skills said subagents do not edit. The
framework's largest available speedup was unreachable by anyone following the
skills as written.

`onto-verify/SKILL.md` said two skeptics were "the floor, not the ceiling";
`references/adversarial.md` said "two skeptics" and "The two skeptics". The
skill and its own reference disagreed.

## Decision

We will apply the write-scope rule at every phase and state it consistently.

- Statements about who edits are **scoped to `execution`**. The orchestrator
  owns every `onto` binary call and all workflow state in both modes.
- **Parallel implementers are a supported option**, named in `onto-build`
  rather than only in a reference: with disjoint file sets and
  `isolation: worktree`, implementers run concurrently — one worktree each,
  merged in plan order, with every checkoff done by the coordinator serially
  after the joins. The five conditions in `subagent-protocol.md` still gate it.
- **Two skeptic lenses are a floor.** Further lenses (security,
  data/migration, contract) and per-capability conformance shards join the same
  parallel batch when the change earns them.
- **New fan-out points:** grounding in open, several review lenses on one diff
  in build, and scenario-evidence gathering in verify, all via read-only agents.

## Consequences

- The division of labor works as designed under `execution: subagent`, where
  the unqualified sentence had been telling agents not to use it.
- Parallel implementers are now discoverable, which means they will be used —
  including by agents that skim the five conditions. This is the one place a
  subagent can corrupt a shared tree, and the guard is prose an agent follows,
  not something the binary checks. Getting it wrong is silent.
- Delegated scenario evidence puts a subagent between the orchestrator and the
  proof. The orchestrator still signs the evidence table, so an explorer that
  reports "passes" without pasting output has produced nothing usable — but
  that discipline is now load-bearing where it previously did not exist.
- More concurrent agents means more tokens per change and overlapping findings
  to triage. onto was already the expensive option; it is now more so.
- Adding lenses is discretionary, so two changes of similar risk may get
  different adversarial depth depending on who ran them.
