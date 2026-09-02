# Use relative targets for same-move project links

- **Status:** Accepted
- **Date:** 2026-09-02

Supersedes the absolute-target clause of
[ADR 0003](0003-owned-content-symlinked-surgical-merge.md); the rest of 0003
(symlink owned content, surgical merge, never clobber) stands unchanged.

## Context

ADR 0003 made every symlink target absolute "to be valid from anywhere."
Moving or renaming a repository breaks every project-scoped link, because
`.opencode/skills/x -> /old/path/.homonto/catalog/skills/x` dangles and is
classified foreign by the raw-prefix ownership check. The documented fix is
deleting links by hand and re-applying. But a project link's source and
destination live in the same repository and move together — the absolute
target buys nothing there.

## Decision

We will store relative symlink targets for links whose source and destination
move together (project scope within one repository: `.opencode/` into
`.homonto/` or `homonto/`). User-scoped links stay absolute — `$HOME` does not
move with the repo. Ownership checks resolve the target against the link's
directory before comparing, and state records semantic link intent (normalized
target data) rather than raw strings. A stale absolute link from a moved repo
is repaired only through the plan path, only when it exactly matches the
recorded prior target and old/new path suffixes agree.

## Consequences

- Renaming a repository and re-applying converges without manual deletion.
- Cross-volume moves (Windows drive letters) fail before writing rather than
  producing broken links.
- Every consumer of link state (status, doctor, prune, adopt) must normalize
  before comparing — raw string equality is no longer an ownership test.
- The migration rewrites recorded link entries once; a link left behind by the
  old encoding is repaired, never silently replaced.
