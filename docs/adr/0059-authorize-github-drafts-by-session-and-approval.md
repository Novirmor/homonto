# Authorize GitHub drafts by session and approval

- **Status:** Accepted
- **Date:** 2026-09-16

## Context

The GitHub draft plugin required every tool call to report the configured
coordinator agent and tied a draft to the staging call's abort signal. OpenCode
ends that signal after the call, and custom skills may run under another agent,
so approved drafts could become unusable even though their session, approval,
and destination checks remained valid.

## Decision

We will authorize GitHub draft tools through a live OpenCode context, the draft's
session identity, its correlated native-question approval, and its existing TTL
and publication checks. A completed staging call's abort signal will not own the
draft lifetime. Any agent that OpenCode permits to invoke the tools may use them
in that session. Generated homonto worker profiles will continue to deny the
tools, preserving their coordinator-owned role boundaries.

We reject enforcing the configured coordinator name again inside the plugin:
that duplicates host permissions and prevents user-defined agents and skills
from completing an explicitly approved publication.

## Consequences

Custom agents can stage, inspect, and publish exact approved drafts without
switching to `homonto`. Session deletion, replacement, expiry, plugin disposal,
permission checks, destination freshness, actor identity, and uncertain-send
reconciliation still block unsafe or stale publication. A custom agent granted
these tools gains publication capability after the native approval; users must
treat that tool permission as intentional authority.
