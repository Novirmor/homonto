# Separate workflows from engines

- **Status:** Accepted
- **Date:** 2026-10-25

## Context

`[frameworks.*]` conflates two different roles. `h` is a GitHub-facing
workflow that can review and publish without a change-state engine, while
`onto` and `to` are engines with separate state machines and binaries. Making
`h` depend on both engines prevents a lightweight GitHub-only installation.

## Decision

Configurations use `[workflows.<name>]` and `[engines.<name>]` instead of
`[frameworks.<name>]`. `h` is a standalone workflow and has no engine
dependency. `onto`, `to`, and the catalog-only `no` are engines. The model may
select an installed engine for a task; configuration does not couple a workflow
to one.

`no` has one direct entry skill and exactly two subagents, implementer and
reviewer. It owns no durable state and ships no executable. Its content must
not invoke `onto` or `to` commands or binaries.

This is a breaking configuration change. Legacy `[frameworks.*]` declarations
are rejected; homonto provides no automatic migration or compatibility layer.

## Consequences

`h` installs independently, and engine installation is explicit. The catalog,
config API, projection, gates, examples, and documentation use the new terms.
Existing configurations must be rewritten before they can be loaded. `no`
trades lifecycle evidence and binary enforcement for model-directed, minimal
development and review.
