# Allow coordinator local Git operations

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The coordinator's Bash profile prompted for destructive local Git operations.
Those prompts interrupted normal repair and workspace work, encouraging indirect
Python or shell-script workarounds even though the trusted shell baseline already
permits arbitrary workspace execution.

## Decision

The coordinator will auto-allow all local Git operations. `git push` remains a
confirmation prompt. Implementers retain their existing destructive-command prompts.

## Consequences

The coordinator can reset, rebase, restore, amend, and manage worktrees without a
tool prompt. Local Git operations can discard work, so workflow dirty-work rules and
agent instructions remain the safety boundary. Publishing still requires confirmation.
