# Separate conflict-only PR resolution

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Conflict-only requests otherwise enter general PR continuation, whose feedback
collection and repair loop can expand into unrelated test hardening. They also
need explicit merge direction and history-preservation rules.

## Decision

Ship `/h-resolve-conflicts` and its skill in the standalone h bundle. Pin the
existing open PR's head and base, probe for actual conflicts, merge the base into
the head without rewriting history, verify the resolution and push normally to
that exact head. Preserve both sides' intent; broader repairs need scope approval
through the native dialog. Reuse h's read-only research/review workers.

No engine or lifecycle record is required. Invocation authorizes only the
verified same-head push, not comments, review-thread resolutions or PR merge.
An already conflict-free pair is a no-op, not an excuse for an update commit.

Reject general continuation for conflict-only work and automatic rebase/force-push.

## Consequences

Users get a narrower authority and verification boundary. Merges add history
rather than rewriting it; moving remote refs and ambiguous semantics can block
delivery. Prompt and materialization tests protect the shipped contract, not
model compliance or live GitHub mergeability.
