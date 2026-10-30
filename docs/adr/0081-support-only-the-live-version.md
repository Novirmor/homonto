# Support only the live version

- **Status:** Accepted
- **Date:** 2026-10-28
- **Supersedes:** [0080](0080-freeze-workspace-migration.md); the in-flight
  `fix`/`tweak` mapping in [0078](0078-merge-onto-presets-into-light.md)

## Context

homonto carried several paths that existed only to read what older releases
or OpenCode 1 left behind: `homonto workspace migrate` with its journal,
receipts, and adopted worktree bindings (about 18k lines); onto's readers for
unversioned and schema 1/2 state files and the pre-binary `state.yaml`, plus a
load-time mapping of the removed `fix`/`tweak` presets; and OpenCode 1
support in the adapter, config, and permission-event parser. ADR 0080 froze
the migration rather than removing it. Every such path has to be tested and
kept correct against formats that are still changing in 0.x.

## Decision

Support only what the current binaries write, for the OpenCode version they
target.

- Remove `homonto workspace migrate`, the `workspacemigration` and
  `migrationrecord` packages, retired-record filtering in status and the onto/to
  CLIs, and migration-adopted worktree bindings. A registry entry with the
  migration origin fails with the fix.
- onto loads only `onto-state.yaml` at the current `schema_version`. Unversioned
  files, earlier versions, a lone `state.yaml`, and the `fix`/`tweak` workflow
  values fail with the fix. The to-state shapes stay as they are: the current
  binaries still write both of them (an unversioned to-state comes from
  `onto demote` on a legacy config).
- OpenCode 2 only. homonto no longer reads OpenCode 1's `tui.json`; the four V1
  TUI spellings fail at load and name their V2 key; the permission-event package
  keeps only the V2 contract. State entries recorded for `tui.json` are dropped
  from state on the next apply, without touching the file.

Config `schema_version` 0/1 layouts stay supported: they are still a live
format, not migration input.

## Consequences

Upgrading across a breaking change means finishing in-flight work with the
release that started it. homonto offers no conversion; errors name the manual
step instead. A large amount of code and its tests go away, and new formats
don't have to keep old readers in step.
