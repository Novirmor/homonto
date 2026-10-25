# Release notes

The `release` workflow publishes this file as the top of each GitHub release
(`--notes-file docs/release-notes.md --generate-notes`), followed by the
generated commit list. Keep it to the release being tagged and the standing
known limitations. When cutting the next release, add its section to
[`CHANGELOG.md`](../CHANGELOG.md) and replace the section below.

## What's in this release

This release ships **three binaries**: `homonto` (config projector), `onto`
(spec-driven workflow operator), and `to` (minimal coding-framework
bookkeeper), for every supported OS/arch as separate archives under one
`SHA256SUMS`. `onto` and `to` each require `homonto` to have installed their
framework first (`[engines.onto]` / `[engines.to]` + `homonto apply`).
Earlier releases: [CHANGELOG.md](https://github.com/noviopenworks/homonto/blob/main/CHANGELOG.md).

### v0.33.0 — OpenCode wildcard service bindings

All three binaries are stamped v0.33.0. The embedded catalog is 0.33.0 (onto
0.25.5, to 0.22.4, h 0.10.4).

- **OpenCode V2 authorization (#26):** GitHub draft preparation accepts service
  metadata registered with an IPv4 or IPv6 wildcard listener and connects only
  through its corresponding loopback client address. Remote hosts, userinfo,
  query strings, fragments, redirects, authentication, PID/version matching,
  workspace plugin identity, cancellation, and request deadlines retain their
  existing checks.

**Upgrade:** install matching v0.33.0 binaries, run `homonto plan` and
`homonto apply` in each configured project, then restart OpenCode. Existing
workflow records do not migrate. A GitHub draft remains behind its existing
native permission and approval flow.

**Validation:** regression coverage includes IPv4/IPv6 loopback and wildcard
registrations plus rejected remote endpoints. The complete pre-tag gate,
including race tests, vulnerability scanning, Docker E2E, and release packaging,
must pass before publication; the release workflow repeats it.

## Known limitations

homonto is a young, deliberately narrow tool. For the current 0.x line:

- **OpenCode JSONC comments are not preserved** on any apply that writes
  `opencode.jsonc` (the file is rewritten as normalized JSON). Accepted for beta.
- **The bundled catalog ships only homonto-native content**: the `onto` and
  `to` frameworks (complementary, ADR 0042), the `h` GitHub companion, and loose framework-agnostic
  skills/commands. Third-party frameworks are not bundled; vendor them via a
  `local:` path or a digest-pinned `remote:` archive (the same fail-closed
  verification `remote:` subagents use). Every `remote:` source requires a
  `digest = "sha256:…"` pin, and homonto never re-resolves a pin to newer
  content on its own.
- **OpenCode is the only adapter.** Claude Code and codex support was removed
  in v0.13.0; a config naming them fails at load naming the key.
- **Secrets require `pass` or an env var** at apply time (`${pass:...}` /
  `${ENV_VAR}`).
- **After moving or renaming a repository, re-run `homonto apply`.** Project-local
  links use relative targets; reapply refreshes absolute config bindings and
  managed user-scoped references. Foreign links are never silently adopted.
- **Workspace migration and bound conversion remain explicit limitations.**
  There is no automatic records-layout migration, and converting a change with
  registered worktrees is refused rather than silently rebinding its ownership.
- **Native permissions are not a filesystem sandbox.** OpenCode v1.18.29 does
  not report an `apply_patch` move destination to edit-permission checks. The
  documented coordinator-only records policy remains binding, but static
  permissions cannot enforce an unreported destination.

See the README's "Caveats" section and
[`docs/guides/troubleshooting.md`](https://github.com/noviopenworks/homonto/blob/main/docs/guides/troubleshooting.md) for details.
