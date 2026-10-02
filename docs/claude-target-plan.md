# Development plan: Claude Code as a separate target

Status: first implementation slice landed on `claude` (see [First implementation
slice](#first-implementation-slice) below for the exact boundary). Experimental
branch: `claude`. Baseline: `b5bf62a` (v0.32.2). No release version is assigned
yet. The branch-specific `claude-experimental-rc.1` testing build does not
assign or bump a stable semantic version. Its release notes are authoritative
for experimental scope and accepted verification gaps.

This plan was grounded in direct source, tests, ADRs, and Git history, not an
OKF bundle. Current code and tests remain authoritative for shipped behavior.
The architectural direction is recorded in
[ADR 0066](adr/0066-add-claude-as-an-explicit-target.md).

## Outcome

Add Claude Code as an explicit `claude` projection and runtime-integration
target alongside `opencode`. Keep the same `homonto`, `onto`, and `to` binaries,
configuration source, workflow state machines, evidence gates, and designated
state home. Do not create a Claude-only product fork or Agent SDK application.

The first complete experimental release includes native installation, a
main-session coordinator, `to` and `onto` workflows with restricted specialists,
and bounded read-only recovery context. Projection alone is an earlier milestone,
not a claim that the workflows work in Claude.

### Compatibility contract

- Omitted `targets` continues to mean OpenCode only. Claude is opt-in.
- A resource can explicitly select `claude`, `opencode`, or both. Target-specific
  model routes and capabilities are validated independently.
- Claude-only desired configuration must not create or inspect unrelated
  OpenCode destinations as an installation prerequisite. Previously managed
  resources removed by a target switch may be pruned through an explicit plan;
  that is different from touching foreign OpenCode files.
- Applying both targets preserves separate tool ownership inside each state
  partition. Removing one target must not remove the other's resources.
- Preserve unmanaged settings, hooks, files, and links. Use existing adoption,
  drift, atomic-write, secret-redaction, and pruning contracts.
- Do not activate historical Claude ownership records merely by registering an
  adapter. Define and test a migration/adoption policy before enabling pruning.
- Unsupported capabilities fail with actionable errors before writes. Never
  silently drop security-relevant agent policy or advertise prompt instructions
  as host-enforced restrictions.
- Workflow state remains writable through the existing binaries, not hooks or
  a second Claude-specific state machine. Existing evidence gates do not become
  stronger merely because Claude invokes them.

### Scope boundaries

| First experimental release | Deferred |
|---|---|
| Project and user scope, including declared repositories | New standalone SDK application |
| Selected Claude settings and stdio MCP | HTTP/OAuth MCP schema expansion |
| Claude-native skills and agent rendering | Marketplace/plugin distribution |
| Main-session coordinator and restricted workers | OpenCode TUI/RPC parity |
| `to`, then full/fix/tweak `onto` | GitHub draft/approve/publish runtime and `h` bundle |
| Read-only snapshot, handoff, and recovery hooks | Permission-learning telemetry |
| Existing homonto-managed workspace bindings | Claude-managed worktree orchestration |

User plugins and unrelated integrations remain unmanaged; they are not removed
to enforce these scope boundaries. Unsupported homonto declarations are rejected.

## Target design

```text
one homonto configuration
    -> shared expansion, planning, ownership, and catalog
        -> opencode adapter + OpenCode rendering/integration
        -> claude adapter + Claude rendering/integration

Claude main session: homonto coordinator
    -> explorer / reviewer / skeptic: read-only tools
    -> implementer: serial, bounded write assignment
    -> onto / to binaries: workflow transitions and evidence gates

Claude recovery hooks
    -> homonto workflow snapshot / handoff: observation only
```

Share host-independent policy and workflow references. Introduce explicit host
variants for tool names, entrypoints, model fields, permissions, and runtime
instructions; do not maintain two independent copies of the workflow doctrine.
The rendering mechanism is decided in M0 and implemented in M4.

Illustrative target selection below is **proposed syntax, rejected by the
baseline release**, not a complete runnable configuration:

```toml
[frameworks.to]
source = "builtin:to"
targets = ["claude"]
```

Using `targets = ["opencode", "claude"]` will select both. Frameworks sharing
the coordinator must have compatible placements; per-target model requirements
must not accidentally require routes for an unselected host.

## Milestones and dependencies

| Milestone | Depends on | Exit evidence |
|---|---|---|
| M0: pin host contract | none | Capability matrix and Claude discovery/permission fixtures |
| M1: target-aware shared plumbing | M0 | OpenCode regression and target-isolation tests |
| M2: Claude projection | M1 | Conformance and safe ownership lifecycle |
| M3: repository/state integration | M2 | Two-target, two-repository lifecycle and recovery tests |
| M4: native agents and catalog | M0, M3 | Host discovery, routing, and permission tests |
| M5: usable workflows | M4 | Real `to` and `onto` lifecycle evidence |
| M6: recovery integration | M5 | Resume/compaction and read-only failure-path evidence |
| M7: setup and release qualification | M6 | Installer matrix, full gate, live Claude acceptance |

Each implementation change carries its focused tests. Check off an item only
after its evidence exists; a rendered file or mocked host response is not proof
of live host behavior. These milestones organize work, not separate promised tags.

### M0 — Establish a tested Claude host contract

- [ ] Pin the minimum tested Claude Code version, initial operating systems,
  and supported permission modes. Record official documentation and captured
  native-format fixtures for that version.
- [ ] Verify project/user paths, configuration-directory overrides, MCP trust
  approval, agent discovery, skill precedence, and session startup behavior.
- [ ] Prove the main-session coordinator activation method. Evaluate native
  named-agent startup rather than assuming OpenCode's command `agent:` field
  translates to Claude. Required user decisions remain in the main session.
- [ ] Map every neutral agent capability: read-only tools, shell rules,
  delegation, dialogs, network/MCP access, repository boundaries, model choice,
  effort, and turn limits. Classify each as supported, explicitly unsupported,
  or policy-only; never silently weaken a requested enforced restriction.
- [ ] Select host-specific catalog rendering and hook response formats. Verify
  hook events against the pinned host; startup hooks are not per-model hooks.
- [x] Define historical Claude state handling and ambiguous physical-destination
  ownership behavior. Reject unsafe overlap before mutation; do not claim locks
  in different configuration homes serialize writes to shared user files.
  **Slice decision (landed, revised by the M3 slice):** adapter selection is
  state-aware — the claude adapter is built when the config explicitly targets
  claude OR a loaded state partition carries claude records. The first slice's
  pure-dormancy rule orphaned records when the last claude declaration was
  dropped (nothing remained that could plan their removal); the state-aware
  gate reconciles them instead: de-declared managed links and MCP entries
  prune under the standard contract, and records under namespaces the adapter
  does not manage (settings, plugins, plugin configs, marketplaces, project
  settings) retire state-only without touching on-disk files. Proven by
  `TestClaudeAdapterSelectionFollowsState`,
  `TestMultiRepoTargetSwitchPrunesOnlyClaude`, and
  `TestStaleStructuredRecordsRetiredStateOnly`. The ambiguous
  physical-destination question (two partitions addressing one file) stays
  open with M3.

Stop a capability at this milestone if the host cannot enforce its advertised
boundary. Do not work around denials with another tool or permission mode.

### M1 — Remove shared OpenCode-only assumptions

Primary areas: `internal/config/{config,load,validate,expand}.go`,
`internal/adapter/registry/registry.go`, `internal/engine/engine.go`, and
`internal/engine/remote.go`.

- [ ] Centralize supported-target selection while preserving the OpenCode-only
  default in all expansion, alias, tune-only, and legacy-agent paths.
- [ ] Introduce target-aware factories and resource enumeration for main and
  named repositories. Keep unimplemented Claude resources rejected until M2/M4.
- [ ] Make automatic OpenCode integration enablement depend on target selection.
  Define explicit override behavior and ensure disabled integrations do not
  reject foreign OpenCode plugin files.
- [ ] Separate tool identity from display labels such as `opencode@repo` for
  state, provenance, and diagnostics.
- [x] Add negative isolation fixtures with foreign target files present and
  preserve unchanged OpenCode-only plans, defaults, and integration behavior.
  **Slice status (landed):** `TestClaudeOnlyLeavesOpenCodeUnwritten` and the
  unchanged full suite cover the OpenCode-preservation half; foreign-content
  preservation is covered by the claude conformance case and
  `TestForeignContentPreserved`.

Acceptance: existing configurations retain behavior; selecting only one target
cannot accidentally provision the other. An unsupported declaration fails before
any write, including catalog materialization.

### M2 — Restore Claude projection on shared infrastructure

Primary areas: new `internal/adapter/claude/`, existing `baseadapter`,
`structproj`, `fileproj`, `copyproj`, `jsoncodec`, `resourcepath`, and adapter
conformance tests. The pre-removal adapter at `d712888^` is a reference, not a
patch to restore wholesale.

- [ ] Implement the pinned native settings, stdio MCP, skills, and agent file
  mappings. Respect project/user scope and Claude configuration-directory
  overrides. Keep plugin/marketplace declarations unsupported.
  **Slice status:** stdio MCP landed (user `~/.claude.json`, project
  `.mcp.json`, `$CLAUDE_CONFIG_DIR` honored for both); settings, skills, and
  agent mappings remain open.
- [x] Implement Plan/Apply/ObserveHashes and resource descriptions using shared
  reconciliation rather than duplicating ownership logic.
  **Slice status:** landed for the stdio MCP namespaces (`mcp.*`, `projmcp.*`)
  plus the file-projection plumbing needed to reconcile historical records;
  `internal/adapter/claude` reuses `baseadapter`/`structproj`/`fileproj`/
  `copyproj` with no second ownership mapping.
- [x] Add explicit Claude conformance registration and fixtures for create,
  adopt, no-op, update, drift, prune, malformed settings, foreign files,
  link/copy modes, and scope changes.
  **Slice status:** the conformance table drives claude through create,
  idempotence, drift/reset, malformed documents, secrets, and foreign-content
  preservation; link/copy modes and scope changes apply to skills/agents and
  ride with their milestone.
- [x] Test secret sentinels stay out of state, diffs, errors, and history.
  Treat native config files separately: resolved credentials there require
  deliberate handling and must not be represented as safe to commit.
  **Slice status:** the conformance secret check covers the claude adapter
  (an MCP env reference resolves on disk only; state records the unresolved
  token). The commit-safety caveat stays open with settings support.
- [x] Apply the M0 historical-state policy. Plain existing OpenCode configs must
  not unexpectedly prune stale Claude records from older installations.
  **Slice status:** state-aware selection — records reconcile (prune/retire)
  rather than orphan, and configs with no claude target and no claude records
  see no adapter at all; see the M0 slice decision above.

Acceptance: a second apply converges; only owned content is removed; unrelated
settings survive. Confirm Claude actually discovers projected resources and
distinguish file installation from MCP trust approval or successful connection.
**Partially proven:** Claude Code 2.1.285 listed a homonto-projected user-scope
server with its exact command and attempted to start it (connection failed only
because the probe executable did not exist — listing is discovery, not
connection). The native format is pinned by the captured fixtures in
`internal/adapter/claude/testdata/` and enforced by `liveformat_test.go`,
including adopt-over-claude-written bytes. Project `.mcp.json` discovery shows
as "Pending approval" until approved in a session — the documented trust step,
distinct from both discovery and connection.

### M3 — Complete multi-target state, diagnostics, and recovery

Primary areas: `internal/engine/{engine,status,explain,snapshot}.go`,
`internal/cli/explain.go`, `internal/state/`, and `internal/snapshot/`.

- [x] Construct one adapter per selected `(tool, repo)` and share the same
  in-memory state object for adapters writing the same repository partition.
  Avoid loading two copies and letting the last save erase the first tool.
  **Slice status (landed):** `RepoTarget.Adapters` holds one repo-mode adapter
  per selected tool sharing the single `state.<repo>.json` partition; selection
  is state-aware (targeted OR records present). Covered by
  `TestMultiRepoClaudeFanout`.
- [x] Make status, doctor, explain, drift, and removal attribution tool-aware.
  **Slice status (landed):** repo drift observation iterates each target's
  adapters and keys each partition by the base tool id; `homonto explain`
  resolves any `<tool>@<repo>` label; doctor reports the claude config
  location when selected. Doctor's per-resource walks remain OpenCode-only
  until resources can target claude (M4).
- [x] Remove hardcoded snapshot tool attribution and deduplicate state
  partitions during capture/recovery. Audit the separate snapshot apply path
  and materialization/integration coverage before claiming atomic restoration.
  **Slice status:** the journal's tool label derives from the changesets with
  actual work (a claude-only apply labels "claude"; single-tool opencode
  applies keep the historical value); `applyTracked` resolves every
  changeset's secrets before any adapter writes (the plain apply's contract),
  so an early tool can no longer commit before a later tool's secret fails;
  and undo/rollback now reverse structured writes from per-key journal
  records (schema 2) instead of re-planning against the current config — a
  later adapter's failure after an earlier tool committed reverses that
  tool's managed values on disk, proven by
  `TestSnapshotMultiToolLaterFailureReversesEarlierWrite`. Byte formatting of
  a reversed document may differ from the original write (values restore, not
  bytes); legacy state-only retirements (pre-V2 tui.*) reverse through the
  partition checkpoint, not the writer. Second review round hardened the
  recovery path: `homonto snapshot recover` accepts crash-mixed partitions
  (per-key before/after comparison — the after image only exists at commit,
  so whole-image comparison refused every real crash), PREPARED changesets
  are reversed best-effort (a crash between a doc write and its commit mark
  leaves the write on disk under a prepared marker; best-effort also
  tolerates reversing a never-applied changeset against a broken file),
  reversal is validated BEFORE any mutation (zero-mutation-on-refusal),
  first-time CREATED links are recorded from the change itself and removed on
  undo/rollback, undo/recover flip changeset rows to rolled-back, and Retain
  budgets rolled-back journals instead of accumulating them forever.
  `RecoverSnapshot` still lacks a direct end-to-end test — its crash-mixed
  acceptance is covered by the partition predicate, not a killed-apply
  rehearsal.
- [x] Test two tools across two repositories: equal resource names, distinct
  routes, target switches, deletion, partial apply, tombstones, rollback, and
  interrupted recovery. Validate snapshot preview and actual restored files.
  **Slice status:** two tools × two repos, shared-partition keying,
  target-switch pruning isolation (`TestMultiRepoClaudeFanout`,
  `TestMultiRepoTargetSwitchPrunesOnlyClaude`), and multi-tool snapshot undo
  plus cross-tool failure rollback
  (`TestSnapshotMultiToolUndoRestoresBothTools`,
  `TestSnapshotMultiToolFailureRollsBackAcrossTools`) landed. The undo
  contract asserted is ADR 0030's: BOTH tools' state namespaces and managed
  file values restore from the journal (create reversed, delete recreated,
  update rolled back — files included). Still open: interrupted-apply
  recovery (`homonto snapshot recover`) under multi-tool journals, and
  plain-apply (non-snapshot) partial failure remains convergent-retry by
  design (ADR 0004).
- [x] Detect conflicting ownership when declarations from different partitions
  address the same physical file. Preserve the designated state home and
  existing locks; do not silently merge ambiguous owners.
  **Slice status (landed):** Build rejects two adapters claiming one
  destination — declared resources (Describe) OR recorded state keys
  (FileForKey) — so a declaration MOVE between partitions fails closed with
  the way out instead of racing an adopt against a delete
  (`TestConfigRepoSelfDestinationConflictRejected`, including the
  move-and-cleanup path).

Acceptance: tool A cannot overwrite tool B's state or prune its files. A target
switch removes only previously owned resources visible in the plan. Existing
OpenCode snapshot behavior is covered, and unsupported recovery cases fail
explicitly rather than claiming success.

### M4 — Render Claude-native agents and workflow content

Primary areas: `internal/agentfm/`, `internal/catalog/materialize.go`, engine
catalog/render code, `catalog/subagents/`, `catalog/skills/`, and
`catalog/commands/`.

- [ ] Add independent Claude model routing and validation. Do not mechanically
  translate OpenCode `variant`/`steps` into Claude effort/turn limits.
- [ ] Materialize the union of selected resources with target-specific variants,
  fingerprints, alias handling, and GC. Test removing one variant while the
  other remains and repairing missing variants. Cover local/remote frameworks.
- [ ] Preserve the distinction between rendered framework agents and standalone
  verbatim agents. Reject incompatible multi-target standalone content unless
  its format is demonstrably valid for both hosts.
- [ ] Adapt coordinator instructions and skill entrypoints; resolve same-name
  command/skill precedence instead of installing both unchanged. Claude output
  must not require OpenCode-only tools or the deferred GitHub runtime.
- [ ] Give read-only specialists a positive tool allowlist excluding execution,
  mutation, further delegation, and write-capable MCP tools. Verify effective
  permissions including inherited settings, not just rendered frontmatter.
- [ ] Validate implementer restrictions without accidentally restricting the
  coordinator. Keep permission semantics explicit; shell rules are not a sandbox.

Acceptance: Claude loads the intended coordinator and workers, routes decisions
to the main session, and rejects forbidden worker actions on the pinned host.
Tests cover both requested-but-unsupported policies and allowed execution.

### M5 — Prove `to`, then `onto`, end to end

Primary areas: shared catalog policy and workflow references; existing
`internal/tocli/`, `internal/ontocli/`, and `internal/workcli/` contracts remain
the backend. Use isolated adopter repositories, never a root homonto config or
projected agent setup in this repository.

- [ ] Execute a real `to` plan/do/done lifecycle with delegated exploration,
  implementation, review, passing verification, and required terminal Git checks.
- [ ] Prove failed verification, a surviving review finding, denied action, and
  missing required user intent do not produce a successful completion claim.
- [ ] Execute full, fix, and tweak `onto` paths, including evidence gates and
  close/integration. Preserve the existing commit/publication authorization
  boundaries; installing a coordinator does not authorize those actions.
- [ ] Test multiple active changes, workflow selection, explicit pauses, worker
  assignment boundaries, and existing worktree bindings. Do not replace them
  with Claude-created worktrees.
- [ ] Capture reproducible fixtures and exact live-host version/results, without
  credentials or sensitive project content. Separate prompt-delivery tests from
  observed model compliance.

Acceptance: both workflows complete legitimate work and expose real blockers.
No workflow file is mutated directly by a hook or alternative state writer.
Missing live credentials/tooling is a release-evidence gap, not a passing test.

### M6 — Add read-only Claude recovery integration

Primary areas: `internal/workflowstatus/`, `internal/cli/workflow.go`, Claude
settings projection, and a thin Claude-specific hook entrypoint chosen in M0.

- [ ] Invoke existing workflow snapshot and exact-generation handoff interfaces
  with the bound config path. Do not independently parse workflow records.
- [ ] Add tested startup/resume/compaction context delivery and an explicit
  status entrypoint. Separate static continuation policy from untrusted artifact
  excerpts and preserve existing output bounds and validation.
- [ ] Bound hook execution time/output; distinguish unavailable observation from
  an empty workspace. Never silently choose between active changes.
- [ ] Test stale generations, moved/nested launch directories, corrupt/unavailable
  records, duplicate delivery, disabled hooks, and preservation of foreign hooks.
- [ ] Never advance state, verify, publish, or force continued execution from a
  hook. Explicit pauses, denials, blockers, and worker return boundaries remain
  valid stops, following ADR 0064.

Acceptance: a resumed session can recover the selected change without changing
it, and observation failure does not claim completion or block unrelated use.

### M7 — Setup, regression coverage, and release decision

Primary areas: `internal/scaffold/`, `internal/cli/init.go`,
`scripts/install.sh`, `scripts/install-test.sh`, `test/docker/`, and host E2E
fixtures under `test/e2e/`.

- [ ] Add explicit OpenCode/Claude/both setup choices, separate from which
  workflow binaries are installed. Generate independent model routes and show
  project versus user/global effects. Preserve existing configuration files.
- [ ] Add OpenCode-only, Claude-only, and combined installer/CLI fixtures,
  including unmanaged settings and resource removal. Keep existing defaults.
- [ ] Document supported Claude versions, startup, permission limitations,
  MCP trust, migration, cleanup, and deferred features after behavior exists.
- [ ] Run focused tests, compile/vet, full regressions, installer and Docker
  suites, and the full gate before tagging. Run live Claude acceptance separately;
  passing the existing gate does not establish host compatibility.
- [ ] Review every unsupported case and outstanding evidence gap before naming
  the experimental release. No automatic commits, pushes, tags, or publication
  are authorized by this plan.

## Verification commands

Run the narrowest applicable checks during implementation; these are existing
commands, not results reported by this planning document:

```sh
go test ./internal/config/... -count=1
go test ./internal/adapter/... ./internal/resourcepath/... -count=1
go test ./internal/agentfm/... ./internal/catalog/... -count=1
go test ./internal/engine/... ./internal/state/... ./internal/snapshot/... ./internal/cli/... -count=1
go test ./internal/tocli/... ./internal/ontocli/... ./internal/workcli/... ./internal/workflowstatus/... -count=1
go test ./internal/scaffold/... -count=1
./scripts/agents-doc-check.sh
./scripts/onto-skills-shell-out-check.sh
./scripts/permevent-check.sh
./scripts/install-test.sh
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./... -count=1 -timeout=20m
./scripts/docker-test.sh
./scripts/gate.sh
```

The full gate repeats several checks above and requires Docker; it is a release
gate, not the default per-edit command. Permission-runtime checks require a
suitable Node version. M0 must establish a reproducible live Claude test command
and prerequisites rather than inventing a currently nonexistent script.

The test matrix must cover target selection (omitted/OpenCode/Claude/both),
scope (user/project/named repo), resource lifecycle, historical state, unmanaged
content, secrets, host permission denials, and interrupted recovery. Report
skips and unavailable live-host evidence separately from successful checks.

## First implementation slice

Start with M0's pinned host fixtures and M1's target-aware selection/integration
tests. Then land a minimal M2 stdio MCP vertical slice through config validation,
plan/apply, ownership, and conformance. Keep workflow framework selection
unsupported until its renderer and native entrypoints are ready. This produces
a testable separate target without pretending the whole port is already usable.

**Landed** (stdio MCP vertical slice + M3 multi-tool repo fan-out + M0 pinned
host evidence):

- Config: `targets = ["claude"]` is valid on `[mcps.*]` (alone or with
  `opencode`), including repo-tagged project scope; resources naming `claude`
  still fail closed naming the mcps-only boundary; `Config.TargetsTool`
  answers adapter selection with omitted targets staying OpenCode-only.
- Adapter: `internal/adapter/claude` projects user-scope servers into
  `~/.claude.json` (relocated by an absolute `$CLAUDE_CONFIG_DIR`) and
  project-scope servers into the config repo's — or a declared repository's —
  `.mcp.json`, using Claude's stdio schema. Empty `args`/`env` are emitted
  because Claude Code 2.1.285 writes them, letting claude-written entries
  adopt as already-desired. File-projection plumbing is wired through
  `baseadapter`/`fileproj`/`copyproj` so historical records reconcile;
  resourcepath carries the claude paths.
- Engine: adapter selection is state-aware — claude is built when targeted or
  when state carries its records, so dropping the last claude declaration
  reconciles (prunes) instead of orphaning. One repo-mode adapter per selected
  tool shares the repository's single state partition. Snapshot journals label
  their tools from the changesets. Doctor checks the claude config location
  only when selected. `homonto explain` resolves `claude@<repo>` labels.
- Shared fix: a structproj delete of a key absent from disk is state-only —
  previously the codec's trailing-newline normalization turned such deletes
  into phantom writes that created never-existing tool files (both adapters
  benefit).
- Host evidence: format pinned by captured Claude Code 2.1.285 fixtures
  (`internal/adapter/claude/testdata/`) with live discovery verified via
  `claude mcp list` against a homonto-projected config.
- Review-hardened (two rounds): snapshot undo/rollback reverse structured
  writes from per-key journal records rather than re-planning the current
  config; a later adapter's failure reverses earlier tools' committed writes;
  recover accepts crash-mixed partitions and reverses prepared-but-written
  changesets; a declaration move between partitions fails closed for BOTH
  structured keys and link records (skill retags included) instead of racing;
  a foreign `.opencode` plugin or malformed OpenCode file no longer blocks or
  warns on claude-only configs (integration preflight and planning are gated
  on the config claiming an OpenCode surface), and a RELINQUISHED integration
  still removes homonto's own bridge link without ever touching foreign files
  or wedging the apply path (`TestRelinquishedBridgeOwnershipConverges`);
  repo-mode records carry apply history under the base tool id; the reserved
  Claude server name `workspace` is rejected; an absolute `$CLAUDE_CONFIG_DIR`
  set after servers were applied refuses to plan rather than stranding them
  in the old registry (unsetting an override remains undetectable — the old
  path left with the environment — and is a documented limitation, as it
  already was for OpenCode's XDG override).
- Known slice gaps, tracked above: settings/plugins/marketplaces stay
  fail-closed at load; `homonto snapshot recover` lacks a killed-apply
  rehearsal test; resourcepath does not honor `$CLAUDE_CONFIG_DIR` for
  skill/command/agent links (nothing can declare them yet); project
  `.mcp.json` trust approval and server connection need a live session, not
  just `mcp list`; a relative `$CLAUDE_CONFIG_DIR` is treated as unset (live
  check of Claude's own behavior owed to M0); path-aliasing (a symlinked
  config root vs canonical repo dirs) can hide a self-repo overlap — exact
  path equality only.
