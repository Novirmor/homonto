# Declare package settings in the manifest

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

`[workflows.*]` and `[engines.*]` decoded into a generic resource with a
source and a scope. A package had no way to accept configuration, and a user
had no way to configure one except by forking it. Config validation runs
before any package code, so a settings schema cannot come from executing
the package.

## Decision

- A schema 2 manifest declares `[settings.<name>]` (type `string`, `integer`,
  `boolean` or `list` of strings, plus `default`, `required`, `enum`,
  `description`) and optionally `[processes]` with `[processes.fields.*]`.
  It is plain TOML, so it is load-safe. The catalog checks the schema is
  self-consistent.
- Users set values under `[<table>.<name>.settings]` and named definitions
  under `[<table>.<name>.processes.<process>]`, on package tables only. Load
  rejects unknown keys (naming the known ones), wrong types, enum misses and
  missing required settings, each at its full key path. Remote packages are
  checked again once their verified root is injected.
- Process definitions are the package's input, kept apart from stages.
  Stages stay engine-owned and declared in `[engine].stages`.
- Packages read resolved values through the binary: `homonto explain settings
  [package] [--json]`. `plan` prints the same lines, with defaults marked. We
  rejected a generated `references/settings.md` per skill: hosting packages
  share skills, and one materialized skill cannot carry two packages'
  values.
- The `no` engine is the first consumer: `isolation` (`ask`, `existing`,
  `worktree`).
- We do **not** bump `schema_version`. A bump guards against an older binary
  silently misreading a newer config, and config decoding already rejects
  unknown fields, so an older binary fails on `settings`, `processes` or
  `[services]` naming the key. No existing shape is replaced, so no old
  shape needs a rejection message. A bump would have rippled through the
  workflow-root markers, the workspace layout and the `onto`/`to` config
  checks for no gain.

## Consequences

Packages become configurable without code, and bad values fail before
anything projects. The schema language is deliberately small. A package that
needs nested tables expresses them as processes, or waits for a schema
extension. Reading settings needs a shell command, so a skill that runs
without one falls back to its documented default behavior.
