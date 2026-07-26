# Gate subagent parallelism on write-scope, not on framework

- **Status:** Accepted
- **Date:** 2026-07-27

## Context

`to` forbade subagent concurrency outright. The rule appeared in all four
subagent descriptions ("Dispatch one at a time — to never runs subagents in
parallel"), in the dispatcher, in both phase skills, and in the design doc,
whose heading read "no parallelization".

The justification given was that concurrent agents race on shared files and
"corrupt `tasks.md`/`plan.md` silently". That reasoning only covers agents that
write. Three of `to`'s four subagents never do: `to-explorer`, `to-reviewer`,
and `to-skeptic` are each documented read-only, keeping bash but never editing.
Only `to-implementer` edits. The rule was three times broader than the hazard
it cited, and it cost real coverage — a single skeptic lens before archiving,
one reviewer per diff, explorers serialized one question at a time.

onto already contradicts the blanket framing: it dispatches two skeptics
concurrently in full mode (ADR 0007).

## Decision

We will decide concurrency by what an agent writes, not by which framework it
belongs to or which agent it is.

- **Read-only agents run concurrently** in both frameworks — explorers on
  separate questions, reviewers and skeptics applying distinct lenses to one
  candidate. They hold no write handle, so they cannot race.
- **Writing agents run strictly one at a time** unless each has its own git
  worktree and a disjoint file set. In `to` this means one `to-implementer`,
  always: `to` keeps a single working tree and deliberately does not adopt
  onto's worktree-per-implementer machinery.
- **Bookkeeping and state stay with the coordinator, serially** — `plan.md`
  and `tasks.md` edits, checkoffs, commits, binary state writes, and branch
  merges are never delegated and never concurrent.

Subagents are used heavily in both frameworks. The distinction between onto and
`to` is artifacts and enforcement, not agent count.

## Consequences

- `to` gains most of onto's fan-out coverage at no correctness cost, since the
  agents newly allowed to run together cannot write.
- "One completed skeptic pass" becomes a floor rather than a ceiling, so a `to`
  change can be attacked from several lenses before `--verified` is asserted.
  The assertion itself is still self-asserted and unchecked.
- Concurrent read-only agents cost more tokens per change and produce
  overlapping findings the coordinator must triage. `to` is no longer the
  cheapest option per change; it is the least ceremonious.
- The transcript stops being a single readable top-to-bottom sequence. That
  readability was named as a feature of `to`; we traded it for coverage, and
  anyone relying on transcript order will notice.
- The safety argument rests on enforcement, not on agent good behavior:
  `read_only: true` in a subagent's `homonto:` block is rendered by
  `internal/agentfm` into Claude's `tools:` allowlist and OpenCode's
  `permission:` map, denying the file-mutating tools in both. An agent that
  cannot edit cannot race, whatever its prompt says.
- That enforcement is also the new failure mode. Flipping a subagent's
  `read_only` to false now silently converts a safely-concurrent agent into a
  racing one, because the concurrency rule reads that flag rather than naming
  agents. Changing a capability profile is a concurrency decision.
