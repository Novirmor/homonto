# Homonto

Homonto governs AI-assisted work in a repository. It issues scoped assignments,
runs configured checks, validates resulting diffs, and records the workflow for
review or recovery.

## Supported Integrations

Claude Code and OpenCode are supported on Linux and macOS. Other tools can use
the public JSON protocol directly, but Homonto ships no integration for them.

## Current Limits

- Use an archive attached to this release or build from its tagged source.
- The write boundary is a host process gate plus final diff validation, not an
  operating-system sandbox.
- Homonto prepares integration branches or staged directories. It does not
  merge them into member branches.
- No user-operable command fetches or installs a self-update. `homonto update
  trust` reports the signing roots compiled into the current binary.
