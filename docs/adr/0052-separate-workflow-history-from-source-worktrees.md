# Separate workflow history from source worktrees

- **Status:** Accepted
- **Date:** 2026-09-07

## Context

The configuration directory was also an implicit source repository. That made
verification and integration depend on its Git state, even when all code lived
in declared sibling repositories. A non-Git OpenCode workspace could not finish
those workflows. An isolated checkout also needed a durable binding so gates
would inspect it instead of the dirty original checkout.

## Decision

Config schema 2 separates the configuration directory, workflow records, and
source repositories. `[repos]` explicitly names every source, including `app =
"."` for a combined project. Schemas 0 and 1 keep their shipped implicit scope.
`[workflow] root` locates records; `git = "existing"` uses existing history and
`git = "managed"` opts into an independently initialized records repository.
`[worktrees] dir` declares a separate allocation parent.

Managed initialization is explicit and refuses unowned populated directories.
Mutating workflow commands checkpoint only records they changed. Markdown-only
edits have an explicit scoped checkpoint. Neither path stages source files,
changes Git identity, disables hooks, or pushes. Failed commits retain a pending
journal for recovery; read-only commands never initialize or checkpoint.

Each selected source has its own immutable base, target branch, identity, and
verification head. Integration receipts belong to sources, not the records
repository. Registered worktrees bind a change and repository to an execution
checkout; gates inspect that binding. Unknown or changed ownership fails closed.

All skills inspect dirty work and offer preserve, isolate, or an explicitly
specified cleanup. A new worktree starts from committed content; it does not
silently copy dirty input or secrets. Preservation is not a cleanliness waiver.

## Consequences

Combined projects continue working, and a non-Git control directory can now
coordinate changes across independent repositories. Records commits no longer
invalidate source verification in managed mode. The cost is a versioned layout,
local identity/binding metadata, and separate source and records recovery.

Changing the config does not migrate existing records or adopt their Git owner.
Automatic migration is not provided. Conversions with registered worktrees are
refused until ownership-safe rebinding is available. The schema-2 allocator has
one binding per change/repository, so same-repository tasks run serially; legacy
isolated parallel execution remains available. Removal never forces away dirty
or unintegrated work and preserves the source branch.

The declared worktree namespaces are trusted writable locations, not a sandbox
for repository scripts. This refines ADRs 0024, 0039, and 0040 for schema 2
without changing legacy configuration semantics.
