# Name the Go module after the canonical repository

- **Status:** Accepted
- **Date:** 2026-10-31

## Context

The repository moved to `github.com/Novirmor/homonto`, but `go.mod` still
declared `github.com/noviopenworks/homonto`. `go install` with that path
worked only through GitHub's rename redirect, which disappears if the old
owner name is ever reused. `go install` with the canonical path failed: the
module declared a different path.

## Decision

We will declare the module as `github.com/Novirmor/homonto`, matching the
canonical owner's case, and use that path in every import, `-X` version-stamp
ldflag, lint exclusion, and document.

## Consequences

- `go install github.com/Novirmor/homonto@latest` (and `/cmd/onto`,
  `/cmd/to`) works without depending on a redirect, once a release carries
  this change.
- Breaking for `go install`: tags up to v0.36.0 declare the old path, so
  they install only as `github.com/noviopenworks/homonto@<tag>`, and newer
  tags only under the new path. Release archives, the installer, and mise
  are unaffected.
- The module proxy stores an uppercase path escaped (`!novirmor`); Go handles
  that transparently.
