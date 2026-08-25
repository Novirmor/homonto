# Homonto workflow redesign

- **Status:** Approved design
- **Date:** 2026-08-24

> **Historical design document.** This is the implementation design the
> rewrite was built from, not a description of the shipped product. The
> shipped surface is [`guides/`](guides/) plus the command list pinned
> by `internal/cli/surface_test.go`. Known deviations: the `rescan`,
> workflow-switching, `attach --take-ownership`, `doctor --recover-lease`,
> and `handoff --portable` commands described here were not shipped, and
> `status` opens the workspace read-write rather than read-only.

## Purpose

Homonto will become an agent-first, human-governed coding workflow product.
Its value is trustworthy delegation: a human supplies an outcome, the host
agent coordinates specialized subagents, Homonto enforces process and fresh
evidence, and the result is left ready for external integration without manual
workflow-state repair.

This is a clean replacement of the experimental product. Homonto will no
longer be a general projector for MCP servers, settings, plugins, arbitrary
skills, commands, subagents, or remote content. It will install only its own
thin Claude Code and OpenCode integrations.

## Product boundaries

Homonto ships one Go binary with two repository workflows:

- **Task** is rigorous execution with minimal human and documentation
  ceremony. It uses `plan -> do -> done` and leaves one checked task record.
- **Change** is governed execution with explicit decisions and durable
  documentation. Its full path uses
  `open -> design -> build -> verify -> close` and it also provides `fix` and
  `tweak` presets.

A workspace selects exactly one workflow. The workflow may be changed only
when no work is active. A Task that expands in scope still finishes as a Task;
there is no active Task-to-Change migration.

Exactly one top-level Task or Change may be active in a workspace. Parallelism
occurs inside that work through subagents and isolated Git worktrees.

Homonto is Git-aware but remains usable when an individual member directory is
not a Git repository. A non-Git member uses isolated directory snapshots and
patch manifests instead of worktrees, commits, and integration branches. The
dedicated integration implementer combines snapshots into a final staged
directory, and verification runs there. Homonto reports the missing commit,
branch, and merge-base guarantees explicitly and never presents a staged
directory as a Git-integrated result.

## Non-goals

The redesigned product will not:

- Project arbitrary third-party resources into AI tools.
- Manage MCP servers, model-provider secrets, plugins, or tool settings.
- Provide a generic configurable workflow language.
- Depend on OpenSpec, Superpowers, Comet, or another workflow runtime.
- Support multiple simultaneous top-level units of work.
- Merge completed integration branches into a base branch.
- Claim that an agent report is truthful or high quality.
- Preserve old commands, state files, configuration, or projected content.
- Support Windows in the first rewritten release.

## Architecture

The binary contains two explicit typed workflow engines over shared runtime
services:

```text
human CLI / host wrappers
          |
versioned JSON action protocol
          |
   +------+------+
   |             |
Task engine   Change engine
   |             |
   +------+------+
          |
SQLite, checkpoints, documents, checks, Git/worktrees, guards, updater
```

The workflow engines own their own states, transitions, required artifacts,
and gates. Shared services provide mechanics only. This avoids both the
current duplicated protocols and a generic workflow abstraction whose valid
states would be difficult to understand and test.

Comet Classic is a design reference for preset routing, explicit decision
points, resumable next actions, binary guards, and bounded repair loops. There
is no code, state, artifact, or runtime compatibility with Comet.

## Workspace model

Initialization scans below the selected workspace root for project directories,
classifies each candidate as Git-backed or non-Git, applies standard
exclusions, and presents the candidates. A non-Git candidate must contain a
recognized project manifest or be added explicitly by path. Homonto persists
only the human-confirmed member list. Later discovery occurs only through an
explicit rescan.

One Git repository owns Homonto's portable configuration and records. During
initialization the user may:

1. Select an existing discovered repository as the control repository.
2. Create a small control Git repository in the parent workspace.

If the workspace root is already a Git repository, Homonto recommends it. A
new parent control repository ignores member repositories and local Homonto
runtime directories.

A Git member stores a local registration under its Git common directory that
identifies the controlling workspace. A non-Git member stores equivalent
registration under a canonical-path hash in the user's Homonto state
directory. Initialization claims registration with atomic create-if-absent;
concurrent initialization has one winner. Existing ownership by another
workspace is rejected even when idle. Reassignment requires an explicit
human-confirmed detach or `homonto attach --take-ownership`, and takeover is
forbidden while an active lease exists.

Starting Task or Change creates a journal entry listing every member, then
acquires active leases in stable repository-ID order. The state does not become
active until every lease is held and the checkpoint is finalized. Failure
releases all leases acquired by that journal operation; crash recovery either
completes the list or releases it. This is all-or-none recovery, not a claim of
cross-filesystem atomicity.

Each lease contains workspace ID, work ID, execution generation, host, process
provenance, and a random local recovery token journaled in the control
database. Every mutating command revalidates all leases. Terminal archive or
abandon releases them. A crashed command leaves its leases; the same workspace
recovers them using the journaled token. Another workspace may clear stale
leases only through `homonto doctor --recover-lease` after a human confirms the
reported owner and checkpoint state. There is no timeout-based automatic
takeover.

Membership changes during active work are applied only through Homonto's
journaled rescan operation. Adding a member claims its registration and lease
before configuration activation; failure rolls both back. Removing a member is
blocked while it has unintegrated assignments, then invalidates downstream
evidence and releases its lease after the committed membership update. Direct
configuration edits remain proposed state until this operation succeeds.

For cross-machine active-work transfer, `homonto handoff --portable` marks the
checkpoint transferable, increments its execution generation, and releases
the old machine's local leases. `homonto attach` consumes that generation and
acquires a new all-or-none local lease set. If the old machine is unavailable,
attach requires an explicit human-confirmed force takeover, increments the
generation, and records the risk; all evidence is stale. An old runtime whose
checkpoint generation no longer matches refuses further mutation when it next
observes the control repository. Homonto cannot prevent an offline stale clone
from being modified, so this is process ownership rather than a distributed
network lock.

Together these rules prevent two local workspaces from concurrently
orchestrating the same working directory and provide an explicit handoff across
clones. Separately cloned repositories remain independent until connected by a
portable handoff.

The control repository contains:

```text
.homonto/config.toml
.homonto/checkpoint.json
docs/homonto/tasks/
docs/homonto/changes/
docs/homonto/adr/
```

Local ignored runtime lives at:

```text
.homonto/runtime.db
.homonto/logs/
.homonto/worktrees/
.homonto/integrations/
```

## Configuration

`.homonto/config.toml` is committed and schema-versioned. It records:

- The selected `task` or `change` workflow.
- The control repository and confirmed member repositories.
- Verification commands, working directories, environment allowlists, and
  timeouts per repository.
- Path classes used for scope and tripwire counting, including test,
  generated, and vendored paths.
- Claude Code and OpenCode model routes for explorer, implementer, reviewer,
  and skeptic roles.
- Whether generated host wrappers are committed.
- Update channel. Release signing roots are compiled into the binary and
  cannot be replaced by repository configuration.

Initialization detects likely test, lint, and build commands from project
files, presents them for confirmation, and stores the approved explicit list.
The latest configuration is used each time verification runs. Each result
records the exact configuration fingerprint and commands used.

Repository membership and verification configuration may change during active
work. Existing evidence becomes stale when the relevant current configuration
no longer matches its recorded fingerprint.

## Command and protocol surfaces

The human-facing CLI uses top-level workflow groups:

```text
homonto init
homonto attach
homonto handoff --portable
homonto task ...
homonto change ...
homonto status
homonto doctor
homonto update
homonto version
```

Invoking commands from the workflow not selected by the workspace fails with
the configured workflow and the safe-switch requirement.

The host-facing contract is a versioned JSON protocol. Human-readable output
is a view over the same domain results, not a separate source of behavior.
The central operation is `homonto next --json`, which returns exactly one
blocking action or one parallel action group. An action includes:

- Protocol version and action ID.
- Workflow, path, phase, and reason.
- Role and complete assignment prompt.
- Repository, working directory, and write scope.
- Parallel-group and dependency information.
- Freshness token and input fingerprints.
- Expected report schema.
- Human decision schema when a decision is required.

Reports are submitted against action IDs and freshness tokens. Stale,
duplicate, unknown, malformed, or wrong-role reports fail closed. The runtime
enforces process provenance, report shape, unresolved findings, and evidence
freshness. It does not claim that report contents are honest.

The runtime emits every currently independent action in one maximal parallel
group. A host that cannot execute the entire group concurrently may defer
actions and execute the group in waves, but it must eventually return one
report for every action ID before the transition can advance. Deferral changes
latency, not required evidence.

Read-only status and resume probes never initialize state, fetch updates, or
perform durable writes.

## Subagent model

Every Task and every Change path uses all four roles:

- **Explorer:** reads the project and reports facts, constraints, affected
  surfaces, and tests. It cannot write.
- **Implementer:** writes only in the isolation area and scope issued for the
  member type, verifies its assignment, and returns integration material. For
  Git this is a worktree commit; for non-Git it is a snapshot patch manifest.
- **Reviewer:** independently reviews the integrated result against the goal,
  artifacts, and acceptance criteria. It cannot write.
- **Skeptic:** attacks assumptions before implementation and claimed evidence
  after implementation. It cannot write.

The host agent launches native host subagents from CLI-provided assignments.
Homonto does not call model-provider APIs. Model choice is configured per role
and host. Host account limits are the only cost and concurrency limits.

Read-only assignments run in parallel whenever their dependencies are met.
Implementation assignments use maximum parallelism in separate isolation areas,
including assignments that may later conflict. Each Git-backed implementer
commits its work. A dedicated integration assignment, using the implementer
role, combines the commits and resolves conflicts. For a non-Git member,
Homonto creates content-hashed directory snapshots; implementers return patch
manifests, and the integration implementer combines them in a final staged
directory. Reviewer and skeptic assess only the integrated branch or staged
directory.

Subagents return questions in their reports. The host agent asks the human and
records the answer through the protocol; a subagent cannot satisfy a human
decision gate itself.

## Task workflow

Task uses:

```text
plan -> do -> done
```

### Plan

Homonto creates `docs/homonto/tasks/<name>.md` with the outcome and a checkbox
list. Parallel explorers inspect the confirmed repositories. A skeptic
challenges assumptions, missing cases, and unsafe scope. The host incorporates
their reports into the same file.

There is no separate plan-approval gate. Consequential unanswered questions
still block implementation and are asked through the host.

### Do

Unchecked work is partitioned into parallel implementer assignments. Homonto
creates isolated worktrees or non-Git snapshots and records their allowed
scopes. Git-backed implementers commit their work; non-Git implementers produce
patch manifests. A dedicated integration implementer combines the outputs into
one integration branch or staged directory per affected member.

### Done

Homonto runs the configured verification commands against the integrated
result, then dispatches reviewer and skeptic assignments in parallel.
Critical and high findings block completion until fixed or explicitly accepted
by the human as documented deviations. Lower findings are recorded but do not
block.

Any repair invalidates affected checks and reports. Verification and review run
again against the new fingerprints. After three failed repair rounds Homonto
requires a human choice to continue, change approach, accept a deviation, or
abandon.

On success Homonto checks every task and appends only:

- A short outcome.
- Exact verification commands and outcomes.
- Integration branch and commit identifiers, or the non-Git staged-directory
  manifest.
- Accepted deviations.

It moves the single file to
`docs/homonto/tasks/archive/<date>-<name>.md`, cleans temporary worktrees, and
leaves integration branches or non-Git staged directories ready for external
handling. It never merges or copies them into the original member.

## Change workflow

Starting a Change first creates a local, uncommitted classification candidate.
Read-only explorer and skeptic assignments may inspect the request and current
project during this preflight. Homonto then suggests `fix`, `tweak`, or `full`,
explains the evidence, and requires human confirmation before portable state or
Change artifacts are created.

### Full path

Full uses:

```text
open -> design -> build -> verify -> close
```

#### Open

Parallel explorers establish current behavior, constraints, and affected
repositories. A skeptic attacks hidden assumptions. The phase produces
`proposal.md`. The human approves scope before Design.

#### Design

The host compares viable approaches and produces `design.md`, `tasks.md`,
acceptance criteria, and identified ADR candidates. The human approves the
design before Build.

#### Build

The phase produces a detailed `plan.md`, partitions tasks into implementation
assignments, coordinates isolated implementer worktrees or non-Git snapshots,
and integrates their commits or patch manifests.

#### Verify

Homonto runs configured checks and dispatches independent reviewer and skeptic
assignments. `verification.md` records acceptance-item results, commands,
findings, repairs, deviations, and residual risks. Failure returns to Build.
Three failed repair rounds require a human decision.

#### Close

Close confirms document, configuration, and implementation freshness; resolves
or accepts blockers; creates required ADRs; writes `record.md`; archives the
change directory; cleans temporary worktrees; and leaves integration branches
or non-Git staged directories ready for external handling.

### Fix preset

Fix handles an existing-behavior defect and uses:

```text
open -> build -> verify -> close
```

Its files are `fix.md`, `tasks.md`, `verification.md`, and `record.md`.
`fix.md` records reproduction, expected and actual behavior, and root cause.
A failing automated test or reproducible command is required before
implementation. When reproduction is not reasonably automatable, the reason is
recorded and human approval is required before Build.

Fix skips deep Design and the full implementation plan unless it upgrades.

### Tweak preset

Tweak handles a bounded behavior, configuration, documentation, or prompt
change and uses:

```text
open -> build -> verify -> close
```

Its files are `tweak.md`, `tasks.md`, `verification.md`, and `record.md`.
`tweak.md` records intent and the exact behavior delta. It skips deep Design
and the full implementation plan unless it upgrades.

### Preset scope and upgrade

Any of these signals pauses Fix or Tweak for a human choice:

- New capability.
- Public API or storage-schema change.
- Cross-module coordination.
- Deep architectural change.
- Scope that should be split into multiple formal changes.
- Material expansion of the human-confirmed intent or acceptance criteria,
  even when no other category matches.
- More than five changed non-test files.

File count is a warning, not an automatic upgrade. It counts unique normalized
paths in the integrated workspace diff from the immutable baseline captured by
the `path-confirmed` transition immediately after the human confirms Fix or
Tweak. A rename counts once. Configured generated, vendored, and test paths are
excluded; all other source, documentation, and configuration paths count.
Continuation, repair, and later path reconfirmation never move this original
work baseline. Verification weight does not decide workflow path.

The human may continue the preset with the broader scope recorded or upgrade
to Full. Upgrade keeps `fix.md` or `tweak.md` as a read-only preset input,
renames the existing `tasks.md` to `preset-tasks.md`, creates `proposal.md`
from the confirmed intent, rewinds to Design, and invalidates preset-only
downstream state. Design then creates new `design.md`, `tasks.md`, and
`plan.md`; human design approval is required before implementation continues.

## Documents and ADRs

Active Change documents live under `docs/homonto/changes/<name>/` and are moved
under an archive directory at Close. Full requires separate proposal, design,
tasks, plan, verification, and final-record documents. Fix and Tweak use
tailored lightweight documents rather than empty Full templates.

An ADR is required only when a Change establishes a durable architectural or
product decision that a future maintainer could reasonably question. ADR need
is assessed during Design and Close. Presets normally upgrade when they
discover architecture, API, schema, or cross-module decisions; if a human
chooses to continue the preset, decision-triggered ADR requirements still
apply.

Changing an approved proposal, design, plan, implementation, repository list,
or relevant configuration invalidates downstream evidence according to stored
fingerprints. The workflow returns to the earliest affected phase rather than
trusting the recorded phase.

The invalidation graph is part of the state-machine definition:

- Task goal or checkbox semantics invalidate Plan outputs and every later
  assignment, check, report, and completion result.
- Full `proposal.md` invalidates scope approval and Design through Close.
- Full `design.md` or an accepted ADR decision invalidates design approval,
  tasks, plan, Build, Verify, and Close.
- Full `tasks.md` invalidates design approval, returns to Design, and
  invalidates plan, Build, Verify, and Close. Full `plan.md` invalidates
  affected Build assignments and all later evidence.
- `fix.md`, `tweak.md`, or preset `tasks.md` invalidates path confirmation,
  reruns the preset scope assessment in Open, and invalidates Build through
  Close.
- Integrated source fingerprints invalidate checks, reviewer and skeptic
  reports, verification, and completion, but do not by themselves invalidate
  approved requirements or design.
- Any repository-membership change returns Task to Plan or Change to Open so
  explorers and the skeptic assess the complete confirmed workspace again. It
  invalidates every later assignment, check, report, approval, and completion
  result. Relevant check-configuration changes invalidate checks, final
  reports, verification, and completion.
- Any test/generated/vendored path-class change returns Task to Plan or Change
  to Open, reruns preset scope assessment against the original work baseline,
  and invalidates affected scopes, assignments, checks, reports, and completion.
- Verification or finding-resolution changes invalidate Close only.

Generated checkpoints and records are not valid user-editable inputs. A
content mismatch is repaired from the database and artifacts or rejected as
corruption; it never advances a workflow.

Document ownership is explicit:

- In Task, the host may edit the goal and checklist only during Plan. During
  Do, only Homonto checks items whose assignments were accepted. During Done,
  only Homonto appends evidence and archive metadata and moves the file.
- In Full, the host writes `proposal.md` in Open, `design.md` and `tasks.md` in
  Design, and `plan.md` in Build. Homonto updates task checkboxes from accepted
  assignments. Homonto generates `verification.md` from evidence in Verify.
  `record.md` and approved ADR-writing assignments are writable in Close.
- In Fix and Tweak, the host writes `fix.md` or `tweak.md` plus `tasks.md` in
  Open. Homonto updates task checkboxes in Build, generates `verification.md`
  in Verify, and generates `record.md` in Close.
- Preset-upgrade renames and generated checkpoint/record writes are
  binary-owned operations. Host edits outside the listed phase and sections
  invalidate the owning action rather than becoming accepted workflow input.

## Human decision points

Homonto pauses only for consequential decisions:

- Confirm Task or Change workspace workflow during initialization.
- Select or create the control repository, confirm discovered members, and
  confirm detected verification commands.
- Confirm Change path classification.
- Approve Full scope before Design.
- Approve Full design before Build.
- Approve a Fix reproduction exception.
- Continue a preset or upgrade it to Full.
- Accept a critical or high finding as a deviation.
- Choose a strategy after three failed repair rounds.
- Answer an unresolved subagent question whose answer changes scope,
  acceptance, implementation authority, or risk.
- Change workspace workflow when no work is active.

Ordinary unambiguous transitions continue automatically. Silence is never
treated as approval.

## Verification and findings

Homonto executes verification itself rather than accepting an agent's claim.
Commands run as explicit argument vectors where possible, from configured
directories, with bounded timeouts and an environment allowlist. The evidence
records command, directory, relevant environment names, exit status, duration,
configuration fingerprint, source fingerprints, and redacted output.

Verification is fresh only when all recorded inputs still match. An integrated
source change invalidates checks and final reviews. A check-configuration
change causes the current configured checks to run and records the new exact
configuration.

Reviewer and skeptic reports use structured severities. Critical and high
findings block. Acceptance of a blocker requires an explicit human decision,
rationale, and inclusion in the committed record.

## Persistence and recovery

`.homonto/runtime.db` is a local SQLite database containing:

- Current runtime projection of workflow state.
- Transition and operation journals.
- Locks and leases.
- Assignment IDs, freshness tokens, and reports.
- Full command output and raw evidence.
- Human decisions and finding resolution.
- Artifact, configuration, and Git fingerprints.

`.homonto/checkpoint.json` is committed and contains only portable stable
facts: schema, workflow and path, phase, logical repository IDs, branches and
commits, execution generation, portable-handoff state, artifact fingerprints,
unresolved gates, and next action. Portable-handoff state is one of `local`,
`transferable`, or `consumed` and identifies the generation that may be
attached. The checkpoint contains no local recovery token, raw report text,
command output, credentials, or secrets. Repository IDs are workspace-assigned
UUIDs associated in configuration with expected relative paths and observed
remotes; paths and remotes are evidence for remapping, not identity by
themselves.

On another machine `homonto attach` discovers members, proposes mappings from
the committed logical IDs, and requires confirmation for ambiguous or changed
paths. It recreates local member registrations and host integrations, then
rebuilds SQLite from the checkpoint, Markdown artifacts, configuration, and
available Git objects. Evidence that cannot be proven fresh is rerun. Missing
raw history does not silently become passing evidence.

Every Homonto-owned filesystem and Git side effect is journaled as prepare,
apply, and finalize. Effects produced inside external host or verification
processes are observed through before/after fingerprints; Homonto cannot
journal their internal writes. Mutating commands are idempotent. Repeating a
command after a crash either completes the pending Homonto operation or rolls
it back to the last stable state. Recovery does not require hand-editing the
database or checkpoint.

Control-plane writes reject symlinked path components, use unique temporary
files and restrictive modes, sync file and directory metadata before reporting
success, and never follow a control destination outside the selected control
repository.

## Host integration and enforcement

The first release supports Claude Code and OpenCode on Linux and macOS.
Initialization installs only the selected workflow's thin command and skill:

```text
/homonto-task
/homonto-change
```

It also installs a read-only resume probe and one write hook. Generated host
files are project-local and ignored by default. The user may opt into
committing them.

Wrappers are installed in the control repository, which is the supported host
launch directory. Invoking Homonto from a Git member locates the control root
through that member's local Git-common-directory registration. A non-Git
member uses its canonical-path registration. Nested or duplicate registrations
fail with the competing control roots rather than selecting one implicitly.

The wrappers call the binary and contain no workflow transitions, required
artifact rules, routing policy, or subagent prompts. The resume probe
automatically resumes one unambiguous active workflow. Unrelated intent or a
conflict requires a human choice.

The write hook delegates to `homonto guard --json`. It is a process gate for
cooperating hosts, not an operating-system sandbox and not a complete security
boundary. It blocks host operations when the host presents them for approval,
and Homonto independently validates resulting repository and artifact diffs
before accepting reports or advancing. It checks:

- Presented source writes occur only during implementation assignments.
- Implementer results contain changes only in issued worktrees or snapshots
  and declared scopes.
- Explorer, reviewer, and skeptic assignments remain read-only.
- Workflow documents are writable only in their owning phase.
- SQLite, checkpoints, and generated records match binary-owned fingerprints.
- Stale or unknown assignment tokens fail closed.

Shell commands and child processes can bypass host hooks. Out-of-scope or
out-of-phase changes detected in final diffs invalidate the assignment and
block advancement; Homonto does not claim to prevent those writes from
occurring in the first place.

## Network and updates

Ordinary Homonto processes perform no network access. Homonto does not call
model, provider, remote-content, or CI APIs. Claude Code and OpenCode remain
separate host processes and may use their normal provider networks.

Only explicit `homonto update` accesses the network. It:

1. Fetches a signed release manifest.
2. Verifies a compiled-in signing root and the artifact checksum. Signing-key
   rotation requires a manifest authorized by an already trusted root and a
   candidate binary carrying the new root.
3. Stages the candidate without replacing the active binary or wrappers.
4. Validates version, protocol, schema, and host-wrapper compatibility.
5. Refuses activation while work is active, tests schema migrations against
   copies of SQLite and the checkpoint, and retains exact pre-update backups.
6. Writes an update journal naming the old and candidate generations, then
   replaces each component with an atomic per-file operation. The candidate
   and prior binary both understand the journal and refuse ordinary commands
   until recovery completes.
7. Writes the activated-generation marker last. A crash before that marker
   causes the next invocation to finish or roll back the journaled generation;
   no cross-filesystem atomic transaction is claimed.
8. Restores the exact prior binary, state, checkpoint, and wrappers if
   activation validation fails. A migration without a tested reverse or exact
   backup restore path is not eligible for self-update.

Automatic update checks are not performed.

## Security and privacy

Raw subagent reports and command output stay in local SQLite. Committed records
contain redacted summaries, hashes, commands, outcomes, decisions, and
deviations. Redaction is allowlist-based for persisted fields rather than a
credential-name denylist.

Homonto treats repository content, command output, Git metadata, archives, and
host reports as untrusted input. Structured protocol values are schema-checked,
paths are resolved against approved roots, and command output is never
interpreted as protocol data.

The runtime enforces process, not truth. Documentation and CLI output must not
claim that structured reports prove model identity, independence, honesty, or
quality.

## Clean-break replacement

The rewrite removes:

- The existing projection engine and adapters.
- MCP, settings, plugin, remote-source, arbitrary skill, command, and subagent
  resource models.
- The separate `onto` and `to` binaries.
- Existing workflow catalogs and substantive phase skills.
- Legacy state, migration, and compatibility code.
- Obsolete product documentation and ADRs.

The existing implementation is not a compatibility surface. Users reset and
initialize the new workspace format. The new thin integrations and runtime are
the only shipped workflow implementation.

## Release acceptance

The rewrite is released as one complete product, not as partial public
milestones. Publication requires:

- Unit tests for every valid and invalid state transition.
- Protocol and state schema tests covering revisions of the rewritten product;
  old projector, `onto`, and `to` formats are explicitly excluded.
- Property and fuzz tests for state, paths, reports, redaction, and recovery.
- Full race testing.
- Crash injection at every journaled operation boundary.
- Multi-repository discovery, confirmation, rescan, and control-repository
  tests.
- Parallel worktree creation, commit, conflict, integration, cleanup, and
  interrupted-operation tests.
- Verification freshness and artifact-invalidation tests.
- Filesystem trust-boundary and secret-redaction tests.
- Signed update, failed activation, and exact rollback tests.
- Native Linux and macOS tests.
- Real Claude Code and OpenCode sessions for Task and for Change Fix, Tweak,
  Full, preset upgrade, repair, interruption, and cross-machine resume.
- Reproducibly pinned tooling for Homonto's own release gate and a clean
  vulnerability scan. Member repositories remain responsible for pinning the
  tools invoked by their configured verification commands.

Passing tests alone do not complete the redesign. The release must demonstrate
the intended user experience: one goal enters the workflow, subagents do the
work, only consequential decisions interrupt the human, fresh evidence gates
completion, and recovery never requires manual state surgery.

## Names and archive collisions

Task and Change names are lowercase ASCII kebab-case, 1 to 64 characters,
starting and ending with an alphanumeric character. `archive`, `active`,
`task`, `change`, `init`, `attach`, `status`, `doctor`, `update`, and `version`
are reserved. The display title remains free-form inside the artifact. Each
active Task file and Change `record.md` carries binary-generated metadata with
its immutable logical work ID and normalized name.

Archives use `<YYYY-MM-DD>-<name>`. A same-day repeated name receives the first
available `-2`, `-3`, and later suffix. Archive lookup reads the portable
record's logical ID and name rather than inferring identity from the directory
suffix.

## Rejected alternatives

- **Keep all three products:** rejected because projection, Task, and Change
  compete for product identity and multiply the security and maintenance
  surfaces.
- **Keep separate `onto` and `to` binaries:** rejected because shared runtime
  behavior had already diverged in durability, locking, and recovery.
- **Use a generic workflow engine:** rejected because only two workflows and
  three Change paths are intended; explicit state machines are easier to audit.
- **Adopt Comet compatibility:** rejected because the useful Classic ideas do
  not require its external skill stack or artifact formats.
- **Commit raw evidence or SQLite:** rejected because reports and command output
  may contain sensitive material and database merges are unsafe.
- **Let Homonto merge final branches:** rejected because external repository
  policy owns integration into the base branch.

## References

- Comet Classic workflow reference: <https://github.com/rpamis/comet>
- Interview method: <https://www.aihero.dev/skills-grill-me>
