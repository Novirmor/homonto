# One declared workspace tmp directory

- **Status:** Accepted
- **Date:** 2026-09-06

## Context

Workflows kept producing transient files — draft bodies, fetched packets,
scratch output — with no sanctioned home. Agents scattered `mktemp` results
that later steps could not find again, and a scratch file landing in the
worktree tripped the dirt gates that guard verification and close.

## Decision

`homonto.toml` may declare a `[tmp]` table naming one scratch directory
(`dir`, default `.tmp`). Declaring it is opt-in; a bare table enables. The
directory must be a relative path below the workspace root, must not be the
root itself or inside `.git`. On every apply, homonto creates the directory,
keeps it gitignored (an anchored entry for locations outside `.homonto/`,
which init already ignores), and generates `references/tmp.md` into the
framework dispatchers and the shared `homonto` knowledge skill naming the path
and the contract. Because the directory sits inside the workspace, every
writable agent — builtin or custom — writes there with no permission-map
changes; read-only workers stay read-only and hand scratch to the coordinator.

## Consequences

A known scratch location replaces anonymous `mktemp` paths for anything a
later step must find. homonto never deletes tmp content: disabling `[tmp]`
withdraws the generated references (the wholesale skill rebuild drops them)
but leaves the directory, its files, and the gitignore entry — cleanup is a
human decision. The generated reference is fingerprinted with the materialize
gate, so a `dir` edit re-projects. An outside-workspace directory was rejected:
it would need per-agent `external_directory` grants, excluding custom agents
and dragging the dirt-gate problem back in.
