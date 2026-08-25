# Adopt a format-agnostic adapter contract for tool projection

- **Status:** Superseded by 0023
- **Date:** 2026-07-12
- **Change:** adapter-contract-codex-pilot

## Context

Each tool adapter re-implemented the same managed-key projection control flow:
compute desired managed-key → value, diff against disk and recorded state to emit
create/update/delete/noop/adopt, surgically merge only managed keys into the
tool's structured file (preserving unmanaged content), and record state. Claude
(JSON) and OpenCode (JSONC) differed only in file format and key mapping. A third
adapter would have copied hundreds of lines of subtle, security-sensitive merge
logic (secret redaction, adopt-vs-noop, drift hashing).

## Decision

We will publish a **format-agnostic adapter contract**: the projection control
flow lives once in `internal/adapter/structproj` (`Project`/`Apply`/`Observe`),
parameterized by a `Codec` that gets/sets/deletes/canonicalizes a value at a key
path in a document. The existing `jsonutil` is the JSON codec; a new `tomlutil`
is the TOML codec. A new adapter supplies only its file path, its desired-value
mapping, and a codec — not the control flow. Values crossing the contract are
JSON-encoded so state hashing stays format-independent.

The pilot adapter is **Codex** (`~/.codex/config.toml`, `[mcp_servers.<name>]`),
built entirely on the contract, proving a third adapter ships without duplicating
the Claude/OpenCode flow. A real-config compatibility fixture is the reusable
conformance test.

## Consequences

- A new adapter is small: file path + key mapping + codec. The security-critical
  merge/diff/redaction logic is shared and tested once.
- Codex projects MCP servers only (the pilot); it is opt-in per resource (default
  targets remain claude+opencode), so existing configs are unaffected.
- Claude/OpenCode are not rewritten in this change (regression risk); migrating
  their structured-file slice onto the contract is a tracked same-behavior
  follow-up. The exit gate — a third adapter without duplicated control flow — is
  met by Codex-on-contract regardless.
- Comment preservation in TOML/JSONC is a documented non-goal (a managed write
  normalizes the file).
