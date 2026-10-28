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
[installation guide](https://github.com/noviopenworks/homonto/blob/main/docs/guides/installation.md).
Earlier releases: [CHANGELOG.md](https://github.com/noviopenworks/homonto/blob/main/CHANGELOG.md).

### v0.35.0 — onto light and live-version support

Breaking workflow and OpenCode compatibility changes. All three binaries are
stamped v0.35.0. The embedded catalog is 0.35.0 (onto 0.27.0, to 0.24.0,
h 0.13.0, and no 0.2.0).

- **Breaking: only the live version is supported (ADR 0081).**
  - `homonto workspace migrate` is removed, along with migration-adopted
    worktree bindings and retired-record handling. A registry entry adopted by
    the migration now fails with the fix.
  - onto reads only `onto-state.yaml` at the current `schema_version`.
    Unversioned or older state files, a lone pre-binary `state.yaml`, and the
    `fix`/`tweak` workflow values fail and say to finish the change with the
    release that created it.
  - OpenCode 2 only: OpenCode 1's `tui.json` is no longer read, and the V1 TUI
    spellings (`theme = "x"`, `scroll_speed`, `scroll_acceleration`,
    `leader_timeout`) fail at load and name their V2 key.
- **Breaking: the OpenCode 1 workflow plugin is removed (ADR 0079).**
  `integrations.opencode.workflow_bridge = true` and an enabled
  `source = "homonto-workflow"` plugin declaration now fail at load; use
  `workflow_context`, on by default with onto, to, or h. A leftover
  `workflow_bridge = false` is ignored. `homonto apply` removes a stale owned
  `.opencode/plugins/homonto-workflow.ts` link. Bundled plugins drop their V1
  `plugin.ts` entrypoints.
- **Breaking: `onto fix` and `onto tweak` are now one preset, `onto light`
  (ADR 0078).** Use `onto new --workflow light` and `/onto-light`; the `fix`
  and `tweak` workflow values, skills, and commands are removed. The preset
  classifies the request itself: a bug still starts from a failing
  reproduction test. **Light never runs a skeptic.** A light change that
  measures over five non-test files, adds a capability, or touches a
  security-sensitive surface escalates to full onto, and `onto set
  verify-scale` / `onto scale --set` refuse `full` for a light change.
- **Worker permission blocks return to the coordinator (ADR 0077).** An
  implementer, reviewer, skeptic, explorer, or h worker whose tool call is
  denied, or whose prompt you reject, stops and hands back a `Permission block`
  naming the exact command or resource. The coordinator first checks whether
  the action is needed and whether its own authority covers it. Then it asks
  you once: run it, send it the output or the missing information, continue
  without it, or change scope. The OpenCode 2 workflow plugin adds a one-time
  notice to the coordinator's context when a worker's prompt was rejected,
  even if the worker returned nothing.
- **Permission handback survives plugin reload and snapshot failure.** Resumed
  workers' parent sessions are discovered on demand with a bounded lookup.
  Rejection notices still reach a validated parent when workflow observation
  fails; changed bindings and out-of-scope sessions do not receive them.
- **Worktree state validation matches onto.** Allocation and recovery reject
  unversioned, old, future, and removed-preset onto states before acting, rather
  than accepting a format the lifecycle CLI cannot read.
- **`onto scale --set` uses the guarded mutation path.** Abandoned and
  archived states are immutable, and the state identity must match the requested
  change. JSON measurement output is preserved.
- **Harness architecture (ADR 0082):** OpenCode remains the primary and only
  shipped harness. Native, tiered support for other harnesses is documented as
  architecture; it is not implemented in this release.

**Upgrade:** finish or abandon unsupported in-flight changes and migrated
worktree bindings with the release that created them before upgrading. Remove
V1 workflow-plugin declarations, replace V1 terminal-setting spellings with
their named V2 keys, and use `onto light` for new bounded changes. Install all
three matching v0.35.0 binaries, run `homonto plan` and `homonto apply` in each
configured project, and restart OpenCode. No state conversion is provided.

**Validation:** the complete automated pre-tag gate passed, including race
tests, vulnerability scanning, all nine Docker E2E suites, and release
packaging. The release workflow repeats the gate before publishing.

**Accepted validation gaps for v0.35.0:** the owner approved publication with
the release checklist's live checks outstanding: the real OpenCode 2 host
(plugin loading, panel, reload/unload, tools, context/compaction, idle
notifications, permission observer, and V1 link removal); native GitHub draft
denial/approval and authorization-boundary checks; real GitHub sandbox sends,
receipts, actor/destination/PR changes, and uncertain-send reconciliation; and
live-host migration checks in both directions. Automated contracts do not
establish these live behaviors. This exception applies only to v0.35.0.

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
  third-party skills or agents as a `local:` path or a digest-pinned `remote:`
  source. homonto never re-resolves a pin to newer content on its own.
- **Secrets need `pass` or an environment variable** at apply time
  (`${pass:…}` / `${ENV}`). Project-scoped MCP secrets end up in plain text in
  the project's `opencode.jsonc`.
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

Details: [README › Scope and limits](https://github.com/noviopenworks/homonto/blob/main/README.md#scope-and-limits)
and [troubleshooting](https://github.com/noviopenworks/homonto/blob/main/docs/guides/troubleshooting.md).
