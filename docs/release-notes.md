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

### v0.36.0 — fresh bases, bring-your-own tools, and safe cleanup

Breaking configuration change. All three binaries are stamped v0.36.0.
The embedded catalog is 0.36.0 (onto 0.28.0, to 0.25.0, h 0.14.0, and no 0.3.0).

- **New changes start from a freshly pulled base (ADR 0083).** Before `onto
  new` or `to new`, the workflow skills ask which branch each repo starts from,
  then run the new `homonto workspace pull --repo <alias> --branch <branch>`.
  It fetches the upstream and fast-forwards only that local branch. It refuses
  a diverged branch, a missing upstream, a failed fetch, or a dirty checkout of
  the branch, and never merges or resets.
- **Draft PRs at any phase (ADR 0084).** The coordinator can open a draft PR
  whatever the verify result or the skeptics say, listing the phase, verify
  result and open findings. A draft records no receipt and changes no gate; at
  close the verified candidate is delivered to it and it is marked ready.
- **`[integration.<name>] repos = ["*"]` covers every declared repository.**
  The check is selected when any repository changes and runs against all of
  their checkouts. `"*"` must be the only entry.
- **Breaking: MCP servers and your own skills and commands are
  bring-your-own (ADR 0085).**
  - `[mcps.*]`, `mcp` under `[settings.opencode]`, and `local:` sources on
    `[skills.*]` / `[commands.*]` fail at load and name the fix. Builtin
    skills and commands, and `local:` agents and packages, are unchanged.
  - Configure MCP servers in your own `opencode.jsonc` and put skills and
    commands in `.opencode/skills/` and `.opencode/command/` (or the
    `~/.config/opencode/` equivalents).
  - Upgrading: delete those tables and apply once. What an earlier release
    projected is released — dropped from state, left on disk unchanged — and
    `plan` shows it as `>`.
  - `homonto explain` no longer has `mcp` / `projmcp` kinds.
- **Cleanup runs at close (ADR 0086).** Closing an onto or to change
  authorizes its cleanup: the coordinator runs `cleanup apply` without asking.
  A worktree whose commits are only on its retained branch (a PR not merged
  yet) is now removable; branches are still kept. Engine-free task cleanup
  follows the same rule and keeps an unmerged task branch.
- **Per-change scratch and `homonto tmp clean`.** A change keeps its scratch
  under `<tmp>/<engine>/<change>/`, and cleanup deletes that folder.
  `homonto tmp clean` previews, and with `--yes` removes, scratch that belongs
  to no active change.
- **Scratch deletion is generation-safe.** Cleanup retains scratch when a
  name belongs to multiple generations. The global sweep refuses incomplete
  workflow inventories, and deletion holds the lifecycle/name lock.
- **Moved-repository handover preserves user links.** Former local skill and
  command links are released unchanged at both project and user scope, even
  when the repository moved before upgrading.
- **`to-done` cleans up after delivery.** Integration verification and confirmed
  source delivery precede cleanup; blocked delivery retains receivers,
  execution worktrees, and scratch for recovery.
- **Release lineage reconciled.** v0.35.0's state-schema guards, guarded
  `onto scale --set`, permission handbacks, and regression tests remain present.

**Upgrade:** remove `[mcps.*]`, `mcp` under `[settings.opencode]`, and standalone
`local:` skill/command declarations from `homonto.toml`; keep their OpenCode
files. Install all three matching v0.36.0 binaries, run `homonto plan` and
`homonto apply`, and restart OpenCode. Released links remain byte-for-byte;
an absolute user link stranded by a repository move must be repaired by its owner.

**Automated release gate:** race tests, vulnerability scanning, all nine Docker
E2E suites, and release packaging are required before tagging. The release
workflow repeats the complete gate before publishing.

**Accepted validation gaps for v0.36.0:** the owner approved publication with
the release checklist's live checks outstanding: the real OpenCode 2 host
(plugin loading, panel, reload/unload, tools, context/compaction, idle
notifications, permission observer, and V1 link removal); native GitHub draft
denial/approval and authorization-boundary checks; real GitHub sandbox sends,
receipts, actor/destination/PR changes, and uncertain-send reconciliation; and
live-host migration checks in both directions. Automated contracts do not
establish these live behaviors. This exception applies only to v0.36.0.

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
