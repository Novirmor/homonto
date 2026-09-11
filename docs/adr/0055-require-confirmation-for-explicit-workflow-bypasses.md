# Require confirmation for explicit workflow bypasses

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The dedicated `/onto-bypass` and `/to-bypass` commands require an explicit user
request, target, and reason, but the coordinator's final Bash deny made the
documented command impossible to run.

## Decision

Direct `onto bypass` and `to bypass` requests will ask the coordinator's OpenCode
permission surface for confirmation. They remain denied for implementers and
undiscoverable to ordinary workflow skills.

## Consequences

An explicit bypass can execute after an OpenCode confirmation and records its
reason in the audit sidecar. A user must confirm a destructive workflow escape
hatch instead of the system silently blocking the dedicated command.
