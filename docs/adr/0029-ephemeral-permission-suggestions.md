# Permission suggestions are ephemeral; nothing learned is persisted

- **Status:** Accepted
- **Date:** 2026-09-02

## Context

Bash approvals during an onto run are the right input for least-privilege
`bash_allow` tuning, but persisting observed commands creates a telemetry
store: commands can carry credentials, paths under NDA, or simply work a user
did not agree to record. Inferring approval from execution is also wrong — a
command running proves nothing about who answered the prompt. OpenCode's
`permission.replied` event is the authoritative decision, correlated with
`permission.asked`. The pinned OpenCode v1.18.29 producer contract
(16747470f976) is captured in offline fixtures, not inferred from the generated
v1 SDK event union or claimed as a live network capture. Requests carry `id`,
`sessionID`, `permission`, and `metadata.command`; replies carry `sessionID`,
`requestID`, and `reply` (`once`, `always`, or `reject`). The earlier
`permission.updated` / `permissionID` / `response` description was incorrect.

## Decision

We will keep observed commands in plugin memory only, per session and
project. Matching Bash requests and replies by session and request ID makes a
command eligible after two explicit `once`/`always` approvals; `reject`
disqualifies it for that session. The plugin removes the candidate and retains
an in-memory deduplication key when it attempts its one suggestion through
`homonto permissions suggest`. That command reads stdin implicitly, with no
`--stdin` flag, and renders a TOML snippet, printing and exiting without writing.
Execution is never approval. The only
persistent artifact is the snippet the user chooses to paste into
`homonto.toml` as `bash_allow_add`.

## Consequences

- No observation log exists to subpoena, leak, or accidentally commit.
- Restarting the session forgets candidates; the threshold starts over.
- The plugin is TypeScript shipped as owned catalog content — homonto projects
  the file but does not execute it, and its contract is fixture-tested.
- Suggestions and `bash_allow_add` stay exact commands; globs are rejected.
  Broader base permissions require a separate deliberate agent-definition edit.
