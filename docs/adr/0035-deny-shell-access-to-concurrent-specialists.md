# Deny shell access to concurrent specialists

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

ADR 0019 treated edit-tool denial as enough to call an agent read-only while it
retained Bash. That agent still held a write handle: shell commands can edit or
delete files, change Git state, and run generators. The capability profile did
not support the concurrency guarantee.

## Decision

Explorers, reviewers, and skeptics in both workflow frameworks will deny Bash as
well as edit tools. They remain concurrent because their available tools cannot
write the candidate. Reviewers receive the candidate diff. Explorers return an
exact query when a conclusion needs a shell-only tool. Skeptics return exact
evidence requests; the coordinator runs each probe and supplies its literal
output for the fresh-context pass.

This supersedes ADR 0019's capability boundary while retaining its rule that
concurrency follows write scope. It also replaces ADR 0020's delegated command
execution with delegated evidence analysis and coordinator-owned execution.

## Consequences

Concurrent specialists cannot mutate a shared workspace through Bash. The
coordinator now runs all requested probes and must preserve their output rather
than paraphrase it. Specialists lose direct Git-history and code-intelligence
CLI access, and skeptics no longer execute an independent command process; their
independence comes from fresh-context analysis of code, claims, and supplied
evidence.
