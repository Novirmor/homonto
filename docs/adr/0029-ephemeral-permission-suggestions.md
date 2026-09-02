# Permission suggestions are ephemeral; nothing learned is persisted

- **Status:** Accepted
- **Date:** 2026-09-02

## Context

Bash approvals during an onto run are the right input for least-privilege
`bash_allow` tuning, but persisting observed commands creates a telemetry
store: commands can carry credentials, paths under NDA, or simply work a user
did not agree to record. Inferring approval from execution is also wrong — a
command running proves nothing about who answered the prompt. OpenCode's
`permission.replied` event is the authoritative decision, verified at pinned
revision 50efc055 (F1).

## Decision

We will keep observed commands in plugin memory only, per session and
project. A command becomes a candidate after two explicit approvals with no
later denial; any denial removes it. The plugin deletes a candidate after
displaying its one suggestion, which `homonto permissions suggest --stdin`
renders as a TOML snippet — printing and exiting, writing nothing. The only
persistent artifact is the snippet the user chooses to paste into
`homonto.toml` as `bash_allow_add`.

## Consequences

- No observation log exists to subpoena, leak, or accidentally commit.
- Restarting the session forgets candidates; the threshold starts over.
- The plugin is TypeScript shipped as owned catalog content — homonto projects
  the file but does not execute it, and its contract is fixture-tested.
- Suggestions stay exact commands; generalizing to globs is the user's edit.
