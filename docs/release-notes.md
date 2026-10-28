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

### v0.34.0 — workflows, engines, and workspace tasks

Breaking configuration change. All three binaries are stamped v0.34.0.
The embedded catalog is 0.34.0 (onto 0.26.0, to 0.23.0,
h 0.12.0, and the new no 0.1.0).

- **`[frameworks.*]` is removed (ADR 0067).** Declare `onto`, `to`, and `no`
  under `[engines.<name>]` and `h` under `[workflows.h]`. A config that still
  names `[frameworks.*]` fails at load; there is no automatic migration.
  `[workflows.h]` no longer installs onto and to or satisfies their binaries'
  install gates; declare an engine yourself if `/h-resolve-issue` should use
  one.
- **`no` engine:** `/no`, one entry skill, `no-implementer`, and
  `no-reviewer`. No binary, no records, and no coordinator of its own.
- **Engines combine (ADR 0068).** onto, to, and no may all be installed in one
  config. A workspace task selects exactly one engine and keeps it on resume.
- **Workspace tasks:** `homonto workspace task create|inspect|check|cleanup`
  records the engine, checkouts, isolation choice, bases, and integration
  results for parallel work across repositories: one repository per
  implementer and one writer per checkout.
- **Integration checks:** `[integration.<name>]` declares `repos`, `repo`,
  `cwd`, and an argv `command`. A check runs against exact candidate
  revisions, records its result, and is invalidated when an input changes.
  Interrupting a check kills its process group and records the result.
- **Terminal cleanup (ADR 0075):** `onto cleanup`, `to cleanup`, and
  `homonto workflow cleanup`, each with `list`, `preview`, and `apply`.
  Cleanup archives an abandoned onto record still in `changes/` and removes
  registered worktrees that are clean and integrated. Eligibility comes from
  native state, never from age or checked tasks. `apply` needs the exact
  generation `--identity` and `--yes`, rechecks every safety condition, and
  writes a retryable receipt under `<workflow-root>/.workflow/cleanup/`.
  Branches and archives are kept. The coordinator inspects cleanup after
  delivery and asks before applying it.
- **Name reuse after abandon:** an abandoned onto archive no longer blocks a
  new change with the same name. When a name is taken, `onto new` and
  `to new` now suggest `cleanup preview`.
- **`/h-resolve-conflicts` (ADR 0076):** resolves only an open PR's merge
  conflicts. It pins head and base, does nothing if they merge cleanly,
  merges the base into the head without rebasing, verifies the result, and
  pushes it to the same head without force. It needs no engine and
  authorizes no comments, thread resolution, or PR merge.
- **Task branches** are named `work-<name>/<alias>`. Cleanup deletes a
  task-created branch once its commits are integrated into the recorded
  target (ADR 0072).
- **Delivery to the requested endpoint (ADR 0069):** the coordinator
  implements, verifies, triages findings, repairs, closes, publishes, and
  confirms the endpoint you asked for, such as a review-ready PR with
  passing required checks.
- **Coordinator permissions (ADR 0070):** ordinary pushes, GitHub API calls,
  routine PR and issue publication, and CI reruns no longer ask. Forced or
  deleting pushes, PR merges, administrative operations, and workflow bypass
  still ask. Workers keep their publication denies; read-only agents keep
  their edit and shell denies. Agent roles now come from the catalog instead
  of hard-coded name lists, and every builtin agent may read an external
  records root.
- **Entry points (ADR 0071):** commands defer execution policy to their
  skills, and `/no` and `/h-resolve-issue` share one execution reference.
  `builtin:example-command` is removed; delete any declaration of it.
- **Proportional repairs and explorers first (ADRs 0073, 0074):** the
  coordinator asks before a repair that is out of proportion to the
  finding, and dispatches explorers instead of guessing about code it has
  not read.
- **Docs:** the README and guides are rewritten. New guides cover
  installation (mise first), concepts, examples, the catalog, workflows, the
  no engine, the h workflow, OpenCode integration, principles, and remote
  sources.
- **Development:** a Taskfile replaces the Makefile and `.mise.toml`;
  `scripts/gate.sh` holds the only golangci-lint pin; CodeGraph is the local
  code index (ADR 0066).

**Upgrade:** rename every `[frameworks.<name>]` table to
`[engines.<name>]` (onto, to, no) or `[workflows.h]`, add
`[engines.onto]` or `[engines.to]` if h relied on one, and remove any
`builtin:example-command` declaration. Then install matching v0.34.0
binaries, run `homonto plan` and `homonto apply` in each configured project,
and restart OpenCode. Existing workflow records do not migrate.

**Validation requirement:** the complete pre-tag gate, including race tests,
vulnerability scanning, Docker E2E and release packaging, must pass. The release
workflow repeats the automated gate before publishing.

**Live-check release exception (v0.34.0 only):** the owner directed publication
without completing these release-checklist checks. None is claimed as passing:

- Interactive OpenCode 2 panel, reload/unload, status/handoff tools, model context,
  compaction, idle notifications, permission observer and V1-link removal checks.
- Native GitHub draft denial/approval, permission prompts and changed-session,
  directory, binding, cancellation and service-mismatch checks in a live host.
- Real throwaway GitHub account/repository sends, matching receipts, changed
  actor/destination/PR OIDs and uncertain-send reconciliation checks.
- Two-way OpenCode migration checks in throwaway projects, including terminal
  settings and snapshot limitations.

This is a one-release exception, not approval to skip these checks in later tags.

## Known limitations

homonto is young and deliberately narrow. For the 0.x line:

- **OpenCode is the only target.** Claude Code and Codex support was removed in
  v0.13.0; a config naming them fails at load and names the key.
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
- **Workspace migration is explicit and partial.** `homonto workspace
  migrate` moves only supported legacy record layouts, and only when you run
  it. Converting a change that has registered worktrees is refused rather than
  rebinding them.
- **Agent permissions are not a sandbox.** OpenCode v1.18.29 does not report an
  `apply_patch` move destination to edit-permission checks, so a move into
  workflow records cannot be guaranteed blocked. The rule that only the
  coordinator writes records still binds; static permissions cannot enforce a
  path the host does not report.

Details: [README › Scope and limits](https://github.com/noviopenworks/homonto/blob/main/README.md#scope-and-limits)
and [troubleshooting](https://github.com/noviopenworks/homonto/blob/main/docs/guides/troubleshooting.md).
