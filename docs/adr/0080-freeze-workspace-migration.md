# Freeze workspace migration

- **Status:** Accepted
- **Date:** 2026-10-28

## Context

`internal/workspacemigration` (about 11k lines without tests) moves supported
legacy records into schema-2 ownership: a reviewed plan hash, a private
recovery journal, captured pre-images, verify and recover actions
([ADR 0058](0058-bind-migration-recovery-to-durable-payloads.md)). That rigor
suits a tool with many users and stable formats. homonto is 0.x and ships
breaking record and config changes regularly (v0.34.0 removed `[frameworks.*]`
with no automatic migration). Extending the machinery to each new format would
make it the largest cost in the codebase while it protects formats that are
still moving.

## Decision

Freeze the migration machinery at its current scope. Supported legacy layouts
keep their `plan` / `apply` / `verify` / `recover` path, and defects in it are
fixed, including recovery-safety and data-loss bugs. New features stay out:
no new source formats or layouts, no new migration actions, no automatic
triggers.

A future breaking change to records or config gets what v0.34.0 used: a load
error that names the key and the manual fix, plus upgrade notes. When a change
cannot tolerate that, it needs its own ADR that revisits this freeze. It does
not quietly extend `workspacemigration`.

## Consequences

The package stops growing, and breaking changes stay cheap to ship. Users of a
newly broken format do a documented manual step instead of an automated,
journaled migration. Revisit at v1, when the record formats are meant to
stabilize.
