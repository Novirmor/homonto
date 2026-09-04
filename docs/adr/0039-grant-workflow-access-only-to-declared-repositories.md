# Grant workflow access only to declared repositories

- **Status:** Accepted
- **Date:** 2026-09-03

## Context

An `onto` or `to` change may select sibling repositories from `[repos]`, but
OpenCode treats paths outside its starting workspace as an approval boundary.
Without a rendered `external_directory` rule, each file operation in a selected
repository prompts despite the configuration already naming that repository.

## Decision

We render resolved `[repos]` paths as `external_directory` allow rules only for
the builtin `onto` and `to` primaries and implementers. Each rendered rule first
denies all external paths, then allows the declared roots. The render fingerprint
includes each agent-to-path association, so `homonto apply` refreshes agent files
after a repository declaration changes. Paths containing OpenCode wildcard
characters (`*` or `?`) are invalid configuration.

## Consequences

Selected repositories become usable workspaces without repeated directory
approval. Read-only specialists and custom agents receive no automatic external
access. A config author grants this access by declaring a Git worktree, so a path
typo fails at config load instead of broadening an agent's runtime access.

OpenCode matches lexical paths and does not resolve symlinks before permission
checks. A declared repository, including links beneath it, is therefore trusted;
this is a workspace-selection boundary, not a filesystem sandbox.
