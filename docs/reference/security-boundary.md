# Security Boundary

Homonto governs a cooperating host's workflow process. It validates work after
the fact; it does not sandbox an operating system process.

## Controls And Limits

| Area | Homonto does | Homonto does not claim |
|---|---|---|
| Write hook | Evaluates a host-presented write against an assignment or edit grant. | Prevent every process from writing outside the hook. |
| Final diff | Checks on-disk changes before it accepts an assignment report or advances. | Undo an out-of-scope write automatically. |
| Workflow documents | Limits edits to the owner and phase for each document region. | Authorize arbitrary direct edits to generated state. |
| Evidence | Stores raw reports and check output locally; portable records carry summaries and hashes. | Treat an agent report as proof of honesty or quality. |

Assignments authorize either a scoped implementer write or no write at all.
Edit grants authorize named regions of one workflow document. Protected
`.homonto/` state remains outside assignment scope.

## Verification Execution

Checks run as argument vectors in configured member directories with bounded
timeouts and environments built from allowlisted names. A timed-out check's
process group is killed.

Captured check output is converted to valid UTF-8 and redacts forwarded values
that are at least six bytes long. Very short allowlisted values are not redacted
because they would match ordinary output too broadly. The evidence records
environment names, not values.

## Filesystem And Input Handling

Control-plane paths that use `securefs` are fd-anchored, reject symlinked path
components, use temporary files, and synchronize file and directory metadata.
The update journal uses a separate pathname-based atomic-write implementation;
do not assume the `securefs` guarantee applies to every Homonto file operation.

Repository content, reports, command output, Git metadata, and archives are
untrusted input. Protocol payloads are schema-checked and strictly decoded.

## Network

No exposed command in the current binary fetches from the network. Homonto does
not call model providers, remote-content services, or CI APIs. Claude Code and
OpenCode remain separate host processes with their own network behavior.

See [Updates](updates.md) for the unwired self-update implementation.
