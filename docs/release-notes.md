# Release notes

The `release` workflow publishes this file as the top of each GitHub release
(`--notes-file docs/release-notes.md --generate-notes`), followed by the
generated commit list. It holds two things: the section for the release being
tagged, and the standing known limitations. When cutting a release, replace
the release section with that version's entry from
[`CHANGELOG.md`](../CHANGELOG.md)
([release checklist](release-checklist.md#3-release-notes)).

## What's in this release

Three binaries, `homonto` (configuration projector), `onto` (evidence-gated
workflow), and `to` (minimal workflow bookkeeper), for Linux, macOS, and
Windows on amd64 and arm64. Each binary is a separate archive, and one
`SHA256SUMS` covers them all. Install with mise or the installer:
[installation guide](https://github.com/Novirmor/homonto/blob/main/docs/guides/installation.md).
Earlier releases: [CHANGELOG.md](https://github.com/Novirmor/homonto/blob/main/CHANGELOG.md).

### v0.37.1 — recover squash-merged PRs

All three binaries are stamped v0.37.1. The embedded catalog is 0.37.1
(onto 0.29.0, to 0.26.0, h 0.15.0, and no 0.4.0).

- Complete a pending explicit-source PR integration after a GitHub.com squash
  merge using `squash-pr:<pr-url>`. The command verifies GitHub's merged PR,
  recorded candidate and target, local receiving history, and exact tree.
  It requires `gh`, a matching origin, and locally available Git objects; it
  does not support GitHub Enterprise or squash results with different trees.

**Upgrade:** install matching v0.37.1 binaries. Existing pending archives can
use the new receipt without state edits when they meet those requirements.

The new GitHub read path was tested with simulated API responses, not a live
sandbox PR. It does not publish to GitHub.

**Automated release gate:** race tests, vulnerability scanning, all nine Docker
E2E suites, and release packaging are required before tagging. The release
workflow repeats the complete gate before publishing.

## Known limitations

homonto is young and deliberately narrow. For the 0.x line:

- **OpenCode 2 is the only target.** Claude Code and Codex support was removed
  in v0.13.0; a config naming them fails at load and names the key. Support for
  other harnesses is architecture only (ADR 0082).
- **`opencode.jsonc` comments are not preserved** by an apply that rewrites
  the file; it is written back as normalized JSON. No-op and link-only applies
  leave it alone.
- **The catalog ships only homonto's own content:** the onto, to, and no
  engines, the h workflow, and standalone skills and commands. Bring
  third-party packages or agents as a `local:` path or a digest-pinned `remote:`
  source. Standalone local skills and commands are bring-your-own. homonto
  never re-resolves a pin to newer content on its own.
- **Secrets need `pass` or an environment variable** at apply time
  (`${pass:…}` / `${ENV}`). MCP configuration and its secrets are managed
  directly in OpenCode, outside homonto.
- **After moving or renaming a repository, run `homonto apply` again.**
  Project links are relative; reapplying refreshes absolute config bindings and
  user-scoped references. Foreign links are never adopted silently.
- **No workspace or onto-state migration.** Finish older work with its original
  release. Config schema 0/1 remains supported because it is still a live format;
  change conversion with registered worktrees is refused rather than rebinding
  them.
- **Agent permissions are not a sandbox.** The coordinator-only records policy
  still applies, but shell commands can execute arbitrary repository code.
  Permission rules do not enforce every effect of an allowed script.

Details: [README › Scope and limits](https://github.com/Novirmor/homonto/blob/main/README.md#scope-and-limits)
and [troubleshooting](https://github.com/Novirmor/homonto/blob/main/docs/guides/troubleshooting.md).
