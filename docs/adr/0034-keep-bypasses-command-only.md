# Keep workflow bypasses command-only

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

ADR 0032 exposed bypasses through skills marked `disable-model-invocation`.
OpenCode does not support that field and skipped those skills because they also
lacked required `name` and `description` metadata. Adding valid metadata would
make the escape hatch model-discoverable, the opposite of the intended boundary.

## Decision

Keep the audited `onto bypass` and `to bypass` binaries and their dedicated slash
commands, but remove bypass skills from the catalog. Each slash command contains
the complete target, reason, warning, and audit contract. Ordinary workflow
resources do not mention bypasses. Direct shell access remains an operator trust
boundary because OpenCode cannot prove who supplied a command line.

## Consequences

The native model skill tool cannot discover a bypass. Users retain a deliberate
emergency entry point and the versioned audit sidecars. There is no skill-level
manual-only claim that OpenCode cannot enforce, but an agent with unrestricted
shell access can still invoke the binary directly.
