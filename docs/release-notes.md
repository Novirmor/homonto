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

### v0.37.0 — configurable final skeptic review

All three binaries are stamped v0.37.0. The embedded catalog is 0.37.0
(onto 0.29.0, to 0.26.0, h 0.15.0, and no 0.4.0).

- Configure `[workflow].final_skeptic` as `off`, `one`, `two`, or `auto`.
  An explicit value applies to `to`, `onto`, and `no`; inspect the resolved
  value with `homonto final-skeptic <engine>`. When omitted, `to` defaults to
  one pass and `onto` and `no` default to two.
- The `onto` light preset and the `no` engine now use final skeptic review;
  `no` gains the read-only `no-skeptic`, which inherits the `no-reviewer` model
  and variant unless given its own model.
- Review instructions record the selected policy and outcomes and refresh
  required passes when the candidate changes. `off` never skips verification or
  explicit review requirements. These instructions do not prove an independent
  review occurred.
- The Go toolchain is updated to 1.26.11 for GO-2026-6859.

**Upgrade:** install matching v0.37.0 binaries, run `homonto plan` and
`homonto apply`, and restart OpenCode. Existing `onto` light and `no` users
should expect two final skeptic passes by default; set an explicit policy if
another trade-off is appropriate. The setting is shared across the three engines.

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
