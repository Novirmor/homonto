# The installer configures new projects

- **Status:** Accepted
- **Date:** 2026-09-05

## Context

The installer could create a bare `homonto.toml`, but a usable workflow setup
also needs a workflow root, trusted sibling repositories, framework choices,
and required model blocks. New users had to discover and write those related
settings before their first `homonto apply`.

## Decision

After `homonto init` creates a new config, `scripts/install.sh` offers a guided
project setup. It asks for the workflow-record directory, existing sibling Git
repositories, `onto` and/or `to`, and one OpenCode model for the main session
and all selected framework agents. The installer validates each selected
repository as a Git worktree and writes it under `[repos]`.

The setup never edits an existing `homonto.toml`. Users review the generated
configuration and run `homonto plan` before any projection occurs.

## Consequences

A confirmed install can leave a new repository with a valid workflow
configuration rather than comments that require manual assembly. Existing
projects retain their configuration ownership, and model selection remains an
explicit user input.
