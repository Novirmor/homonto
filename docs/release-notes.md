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

### v0.36.1 — safer cleanup, remote sources, and installation

All three binaries are stamped v0.36.1. The embedded catalog is 0.36.1
(onto 0.28.0, to 0.25.0, h 0.14.0, and no 0.3.0).

- **Preserve user files and links.** Atomic config writes refuse unresolved
  symlinks instead of replacing them. Copy-mode removal refuses user-replaced
  symlinks and directory traversal through symlinked parents. Backups obey the
  same removal boundary and refuse foreign `.bak` symlinks. Fallback skill and
  command cleanup prunes only managed links.
- **Keep scratch cleanup confined.** Canonical-path checks reject scratch
  directories that overlap repository, workflow, or other owned trees.
- **Validate complete remote archives.** Gzip checksum, truncation, and size
  checks cover the full decompressed stream. Standard tarballs containing a
  `./` root directory are accepted without weakening traversal checks.
- **Redact remote credentials in failures.** HTTP errors, redirects, Git
  diagnostics, malformed URLs, and `remote:`-prefixed locators no longer expose
  the credentials covered by locator redaction. Original transport error causes
  remain inspectable.
- **Handle filesystem aliases and config encoding.** All three binaries resolve
  symlinked working directories and explicit directory/config roots. A UTF-8
  byte-order mark at the start of `homonto.toml` is accepted.
- **More reliable diagnostics and spec merging.** Config validation, missing
  secret references, and foreign-link errors report offenders deterministically.
  Missing onto layout directories name `onto init` as the repair. Fenced code in
  specs is preserved as body text during delta merges.
- **Canonical installation paths.** Release downloads use `Novirmor/homonto`.
  **Go install path change (ADR 0088):** use
  `github.com/Novirmor/homonto@v0.36.1`, with `/cmd/onto` and `/cmd/to` for the
  workflow binaries. Tags through v0.36.0 retain the old
  `github.com/noviopenworks/homonto` module path.
- **Pinned development tools.** `mise trust && mise install` provisions the
  development toolchain, and the gate checks for pin drift.

**Upgrade:** install all three matching v0.36.1 binaries, run `homonto plan`
and `homonto apply`, and restart OpenCode. No configuration or workflow-state
schema change is introduced by this patch.

**Automated release gate:** race tests, vulnerability scanning, all nine Docker
E2E suites, and release packaging are required before tagging. The release
workflow repeats the complete gate before publishing.

## Known limitations

homonto is young and deliberately narrow:

- **OpenCode 2 and Claude Code are the targets.** Claude Code runs every
  engine with the limitations accepted in ADR 0105, which `homonto doctor`
  reports. Codex and Antigravity are architecture only (ADR 0082). The
  supported host × harness × engine cells, and the evidence each needs, are in
  [supported combinations](https://github.com/Novirmor/homonto/blob/main/docs/guides/supported-combinations.md).
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
