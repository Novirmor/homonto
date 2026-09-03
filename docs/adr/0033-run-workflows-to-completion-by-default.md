# Run workflows to completion by default

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

The workflow prompts treated proposal review, technical approach, isolation,
build mode, test mode, phase changes, and close preparation as user gates. An
agent could establish most of those answers from the request and repository, but
still stopped for approval. Direct phase commands also ran under whichever agent
was selected, so they could miss the workflow primary's policy and delegation.

## Decision

Starting or resuming `onto` or `to` authorizes the primary agent to continue
through the remaining lifecycle unless the user names an endpoint or asks to
pause. The primary investigates first, chooses safe reversible technical
defaults, and asks only for missing product intent, an obligation waiver,
acceptance of a known deviation, destructive recovery, or unattributed work that
blocks safe progress. Existing onto evidence fields remain, but record the
coordinator's review and basis rather than claiming every decision came from the
user. Every workflow slash command routes to its primary agent.

## Consequences

Routine changes no longer stop at proposal, approach, plan-ready, phase, or close
checkpoints. Users still decide product scope and explicit exceptions. A wrong
agent default can travel farther before correction, so artifacts retain review
tokens, phase commits, focused tests, and independent review. Explicit endpoints
and pauses remain available.
