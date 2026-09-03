# Development plan - v0.15.0 (planned)

> One release containing seven capabilities. Work may land as focused commits,
> but no capability ships under a separate tag. The release stays blocked until
> every task and the final gate are complete.

## Release contract

- Tag: `v0.15.0`.
- Preserve existing configuration and state through explicit migrations.
- Keep plain `homonto apply` behavior unchanged. Snapshots remain opt-in.
- Keep onto and `to` mutually exclusive after promotion.
- Homonto must not copy values it resolves from secret references into a new
  record. Persisted handoffs contain metadata, evidence stores hashes rather
  than command arguments or output, and the permission plugin deletes each
  in-memory candidate after it displays one suggestion.
- Never replace a foreign file or symlink.
- Treat Git as the source for authorship and time in onto records.
- Block the release if OpenCode has no authoritative permission-decision event;
  execution alone is not proof that a user approved a command.
- Complete the focused tests for an implementation task in the same commit as
  that task. Record the command and result before checking the task.

## Task dependency graph

| Chain | Required predecessor |
|---|---|
| F1; D1; D3; D4 | none |
| D2 | D1 |
| D5 | F1 |
| D6 | D1, D2 |
| F2 | D1 |
| F3 | D1, D3, D6 |
| H1 -> {H2, H3} -> H4 -> H5 | D3, F3 |
| E1 -> E2 -> E3 -> E4 -> E5 -> E6 | D1, F2, F3 |
| L1 -> L2 -> L3 -> L4 -> L5 -> L6 | D2, E2, E4 |
| G1 -> G2 -> G3 -> G4 -> G5 -> G6 -> G7 | D3, H2, F3 |
| P1 -> P2 -> P3 -> P4 -> P5 -> P6 | D4, H4, F3 |
| A1 -> A2 -> A3 -> A4 -> A5 -> A6 -> A7 -> A8 | D5, F1 |
| S1 -> S2 -> S3 -> S4 -> S5 -> S6 -> S7 -> S8 | D6, E6, L6, F3 |
| R1 -> R2 -> R3 -> R4 -> R5 -> R6 -> R7 -> R8 | H5, E6, L6, G7, P6, A8, S8 |

## Foundation

- [ ] **F1 - Verify OpenCode permission telemetry.** Pin the inspected OpenCode
  revision and event name. Commit an upstream event fixture, a parser contract
  test, and a gate script that fails when the fixture or pinned revision is
  absent. Prove that allow and deny carry the session, project, agent, and
  command. If the event lacks any field, stop the release.
- [ ] **D1 - Decide state provenance.** Write the ADR for one state schema that
  covers resource origins, last-change data, bounded tombstones, and semantic
  link intent. This ADR owns the only `.homonto/state*.json` migration.
- [ ] **D2 - Decide relative project links.** Write the ADR that supersedes ADR
  0003's absolute-target clause. Define relocation domains and the evidence
  required before homonto repairs a stale link.
- [ ] **D3 - Decide structured workflow records.** Write the ADR for versioned
  handoff and evidence sidecars. Keep Git responsible for authorship and time.
- [ ] **D4 - Decide workflow promotion.** Write the ADR that reverses the
  no-escalation decision in `docs/to-framework-design.md` while retaining
  framework exclusivity.
- [ ] **D5 - Decide ephemeral permission learning.** Write the telemetry and
  privacy ADR. Keep observed commands in plugin memory; persist only a command
  that the user adds to `homonto.toml`.
- [ ] **D6 - Decide transactional snapshots.** Write the ADR that qualifies ADR
  0004 for opt-in snapshot mode and leaves plain apply's partial-success
  semantics intact. Replace lockfile ownership with a cross-platform process
  lock that the OS releases on process death; retain holder metadata for
  diagnosis.
- [ ] **F2 - Add legacy state fixtures.** Commit main and named-repository state
  fixtures from `v0.13.0` and `v0.14.0`. Test absence, rather than migration,
  for sidecar formats those releases did not have.
- [ ] **F3 - Add operation identity.** Introduce injectable operation IDs and
  clocks for state history, handoff filenames, evidence, promotion staging,
  journals, and tests. A no-op does not create an operation or change history.

## 1. Replayable handoffs

Goal: a new session can recover the exact workflow state and safe next command
without parsing prose.

- [ ] **H1 - Add two handoff views.** The interactive JSON view may include the
  native state for compatibility. The persisted recovery view uses an explicit
  field allowlist: tool and change identity, workflow, claimed and derived
  phase, phase mismatch, dependencies, repository aliases, base/head commits,
  machine gate IDs, artifact paths and hashes, and generated next argv. It
  excludes directives, evidence text, plan excerpts, and other free-form state.
  Reject unknown major versions.
- [ ] **H2 - Add `onto handoff --json`.** Preserve current text output and
  produce H1's envelope. JSON contains no artifact excerpts.
- [ ] **H3 - Extend `to handoff --json`.** Keep its existing JSON keys and add
  the versioned envelope fields so current consumers do not break.
- [ ] **H4 - Persist the recovery view.** Support `--write` for both binaries.
  Write unique operation-ID filenames under the owned workspace, reject
  symlinked parents and existing destinations, and omit arbitrary prose
  excerpts from persisted Markdown and JSON. Keep interactive stdout separate.
- [ ] **H5 - Prove the handoff contract.** Golden tests must show complete onto
  state in interactive JSON, stable persisted JSON, unchanged default text
  output, stale artifact detection, no destination overwrite, parent-path
  confinement, and absence from persisted files of sentinels placed in
  artifact prose and free-form state.

## 2. Resource explanations

Goal: `homonto explain` names why a resource exists, who declared it, where it
projects, what changed, and what would remove it.

- [ ] **E1 - Preserve origins during expansion.** Carry direct declaration,
  framework owner, catalog provider, source class, scope, and repository alias
  through config expansion instead of discarding framework provenance.
- [ ] **E2 - Introduce `ManagedResource`.** Generate typed descriptors from the
  same adapter code that generates desired keys. Do not create a second key or
  destination mapping.
- [ ] **E3 - Add typed change causes.** Distinguish declaration, adoption,
  update, drift repair, disablement, relocation, repin, mode/scope move, and
  removal in plans and state.
- [ ] **E4 - Migrate state once.** Implement D1's schema. Store one last event
  per live resource, semantic link intent, and the latest 100 removal
  tombstones in operation order. A no-op leaves these fields unchanged. Legacy
  entries load with unknown provenance.
- [ ] **E5 - Add `homonto explain`.** `homonto explain` lists all resources;
  `homonto explain <kind> <name> [--repo alias] [--json]` selects one. Sort JSON
  output, return nonzero for unknown selectors, and list candidates on
  ambiguity.
- [ ] **E6 - Prove explanation behavior.** Test each OpenCode namespace, direct
  and transitive framework origins, main and named state partitions, adoption,
  removal, repin, a full tombstone ring, legacy unknowns, selector failures,
  and a resolved-secret sentinel that must not appear in output or state.

## 3. Portable projections

Goal: project-scoped links survive a repository move and legacy absolute links
repair through the normal plan and confirmation path.

- [ ] **L1 - Define relocation domains.** Mark links whose source and
  destination move together. Keep user-scoped and independently moving links
  absolute.
- [ ] **L2 - Use semantic link intent.** Make file projection consume the
  migrated kind, resource, scope, source class, and normalized target fields
  from E4. Do not add another state version.
- [ ] **L3 - Render relative project links.** Resolve targets against the
  destination directory for comparison, ownership checks, status, doctor, and
  pruning.
- [ ] **L4 - Plan legacy repairs.** Authorize repair only when the stale link
  exactly matches recorded prior state and old/new path suffixes agree. Refuse
  regular files, unknown links, traversal, and Windows cross-volume cases.
- [ ] **L5 - Preserve de-declaration behavior.** Removing a declaration after a
  repository move must prune its link without leaving an orphan.
- [ ] **L6 - Prove relocation behavior.** A whole-repository move followed by
  plan/apply must converge for local and builtin links. Separate tests must
  show user links stay absolute, independent repo moves require repair,
  cross-volume Windows links fail before writing, foreign destinations remain
  untouched, and a second apply is a no-op.

## 4. Requirement-to-evidence graph

Goal: onto can trace each capability, requirement, and scenario to tasks,
commits, and verification evidence.

- [ ] **G1 - Add stable artifact IDs.** Add `Requirement-ID:` and `Scenario-ID:`
  markers to templates and validators. Generate IDs once; heading edits do not
  change them.
- [ ] **G2 - Extract an artifact parser.** Parse delta specs, living specs,
  tasks, and verification references into typed nodes without coupling the
  parser to delta merging.
- [ ] **G3 - Add `.onto/evidence.json`.** Store task ID, scenario ID, executable
  name, command hash, repository alias, commit, operation ID, exit status,
  output hash, and artifact hash. Store neither argv nor output. Reject unknown
  major versions. On first write, refuse an existing file without the expected
  schema and change ID. On updates, use confined atomic control-plane writes
  and reject symlinked parents or destinations.
- [ ] **G4 - Add `onto evidence record`.** Accept task/scenario IDs, an
  executable name, precomputed command hash, exit code, and an output file to
  hash. Record the current commit. Never execute a command through `onto`,
  because `onto *` is already allowlisted by the host.
- [ ] **G5 - Add `onto trace [change] [--json]`.** Emit typed nodes and edges for
  changes, capabilities, requirements, scenarios, tasks, commits, and evidence.
  Preserve existing `onto graph` output.
- [ ] **G6 - Add diagnostics.** Make `onto doctor` report missing task/scenario
  bindings, duplicate IDs, orphaned records, unavailable commits, and changed
  artifact hashes. A rebase or squash makes affected evidence stale and
  requires a new record. Legacy changes without sidecars receive a warning.
- [ ] **G7 - Prove graph integrity.** Test a heading rename with stable IDs, ID
  collision refusal, task-to-commit edges, failed-command evidence,
  cross-repo records, stale records after rebase, unknown sidecar versions, and
  evidence hashes in the handoff envelope. Plant a destination symlink, a
  parent symlink, and a foreign regular sidecar; each case must fail without
  touching its target.

## 5. Promote `to` work into onto

Goal: preserve a growing `to` task when it needs onto's design, evidence, and
handoff process.

- [ ] **P1 - Define the conversion contract.** `to promote <name> [--as name]
  --yes` creates a full onto change in phase `open`; it does not claim that
  design or verification already happened.
- [ ] **P2 - Lock source and destination.** Acquire the existing `to` workspace
  lock, then a destination-name lock shared with `onto new`, in that fixed
  order. Hold both through the final rename.
- [ ] **P3 - Build a confined staged converter.** Create a mode-`0700` regular
  directory under `docs/.to-promote/<operation-id>` with create-only, no-follow
  operations. A manifest records canonical source and target names plus every
  source hash. Move the complete source under `imported-to/` unchanged and
  generate canonical onto state and proposal from source plus manifest data.
- [ ] **P4 - Authenticate recovery.** A retry accepts staging only when every
  parent and manifest is a regular owned object and source/target names and
  imported source hash matches. It removes and regenerates onto state and
  proposal rather than trusting staged generated files, and refuses unexpected
  paths. Before allocating a new operation ID, promotion resumes the matching
  staged manifest. It reports an already-finished promotion as success and
  refuses tampered or unrelated staging.
- [ ] **P5 - Preserve framework exclusivity.** Do not install both frameworks.
  Extend onto handoff discovery to `imported-to/`, then print the required
  `homonto.toml` swap, `homonto apply`, and `/onto` next steps after promotion.
- [ ] **P6 - Prove migration safety.** Compare every imported source byte,
  inject failure before each rename, and contend promotion against `to new`,
  `phase`, `done`, and `abandon`. Also test destination collision, repo aliases,
  planted staging symlinks, a tampered manifest, altered generated files,
  unexpected staged paths, handoff discovery, and final onto archival.

## 6. Permission learning

Goal: turn explicit OpenCode Bash approvals into reviewed, least-privilege
allowlist suggestions.

- [ ] **A1 - Parse the pinned event.** Add a small Go/TypeScript contract around
  F1's fixture and fail closed when the decision, session, agent, project, or
  command is missing.
- [ ] **A2 - Add framework plugin declarations.** Extend framework manifests and
  catalog expansion with owned OpenCode plugin files, including collision and
  dependency rules.
- [ ] **A3 - Project plugin files.** Materialize, link, fingerprint, prune, and
  garbage-collect the declared plugin with the same foreign-file protections as
  other owned content.
- [ ] **A4 - Aggregate approvals in memory.** Keep candidates per OpenCode
  session and project. Suggest a command after two allows with no later deny;
  any deny removes that candidate. Delete the candidate after displaying its
  first suggestion. The user accepts by adding the snippet to config or
  discards it by doing nothing. Write no observation log.
- [ ] **A5 - Render suggestions without storage.** Send the candidate to
  `homonto permissions suggest --stdin`; print a TOML snippet and exit without
  writing. Reject shell operators, redirection, assignments, control bytes,
  sensitive flag names, and OpenCode pattern metacharacters.
- [ ] **A6 - Add `bash_allow_add`.** Accept exact commands at
  `[subagents.<name>.opencode].bash_allow_add`. Merge reviewed commands into the
  framework agent's base allowlist, deduplicate them, and include them in the
  render fingerprint.
- [ ] **A7 - Keep denials authoritative.** Reject an additive allowlist when the
  agent declares `bash: false`; reject pattern syntax in exact additions; keep
  `"*": ask` before exact allows.
- [ ] **A8 - Prove permission behavior.** The required fixture test must cover
  allow, deny-after-allow, two-allow threshold, separate sessions and projects,
  candidate removal after display, unsafe input rejection, exact-pattern
  rendering, config parsing at A6's declared path, and no plugin filesystem
  writes. Keep a live OpenCode smoke as extra evidence, not as the release gate.

## 7. Transactional snapshots and undo

Goal: snapshot-mode apply can roll back failures, and a later undo restores
only homonto-managed state without clobbering unrelated configuration.

- [ ] **S1 - Enumerate mutation kinds.** Define structured-key merge, symlink
  create/replace/delete, copy create/replace/delete and backup, catalog
  activation/GC, remote activation, remote-lock write, main/named state write,
  and manifest GC. Exclude additive verified caches. Treat revocation quarantine
  as an irreversible security operation outside the transaction.
- [ ] **S2 - Build a complete mutation inventory.** Reuse `ManagedResource` for
  adapter effects and add engine-owned descriptors for each catalog, remote,
  lock, state, and GC mutation from S1. Prepare and validate the full list before
  the first active write.
- [ ] **S3 - Store semantic checkpoints.** Record unresolved managed values and
  content-addressed owned blobs, not complete tool files or resolved secrets.
  Retain the latest 10 successful snapshots; retain incomplete journals until
  recovery; remove an unreferenced blob only after the manifest GC pass.
- [ ] **S4 - Add `homonto apply --snapshot`.** Write a durable journal, commit
  prepared mutations, and roll back an ordinary failure. Plain apply must not
  create a journal and must retain its existing partial-success behavior.
- [ ] **S5 - Add `homonto recover <apply-id>`.** `doctor` reports an incomplete
  journal and the exact recovery command. Each journal entry records prepared,
  committed, or rolled-back state. Recovery requires committed entries to match
  after-images and uncommitted entries to match before-images, then rolls back
  committed entries in reverse order. It acquires D6's process lock, which a
  killed apply no longer owns. A retry converges.
- [ ] **S6 - Add `homonto undo [apply-id]`.** Acquire the apply lock, render a
  reverse plan, confirm unless `--yes`, verify current after-images, then restore
  mutations in reverse order. Restore prior desired state rather than
  pre-apply disk drift.
- [ ] **S7 - Refuse unsafe recovery.** If an entry differs from the image its
  journal state requires, make no mutation. Validate every manifest path
  against current managed roots, reject planted symlinks, preserve unrelated
  keys, and never reactivate revoked remote content. Crash recovery re-resolves
  a prior secret reference; it refuses without mutation if resolution fails and
  may restore a rotated current value rather than unavailable historical bytes.
- [ ] **S8 - Prove transaction behavior.** Inject one failure for every S1
  mutation kind and assert rollback of all reversible committed entries.
  Separate tests must prove partial-journal recovery, idempotent retry, main plus
  named state restoration, unavailable and rotated secret behavior, user-edit
  refusal, tampered-journal refusal, 10-snapshot retention, blob reference
  safety, irreversible revocation quarantine, process death followed by
  recovery, concurrent apply/recover exclusion, absence of resolved-secret
  sentinels, and unchanged plain-apply semantics.

## Integration and release

- [ ] **R1 - Update command and config references.** Document each new command,
  flag, selector, JSON version, config field, and exit behavior with examples.
- [ ] **R2 - Update workflow guides and prompts.** Teach onto and `to` to consume
  handoffs, record evidence, promote work, and review permission suggestions
  without weakening existing gates.
- [ ] **R3 - Update migration and recovery guides.** Document state migration,
  link relocation, snapshot conflicts, journal recovery, retention, privacy
  limits, and rollback behavior.
- [ ] **R4 - Update versions and release notes once.** Bump the embedded catalog
  and both framework versions. Describe all seven capabilities and their opt-in
  or migration behavior.
- [ ] **R5 - Run repository verification.** Run `go test ./...` and `go test
  -race ./...`; record all failures and skips. The permission contract fixture
  and pinned-revision check must run here without network access.
- [ ] **R6 - Run Docker lifecycle coverage.** Exercise explain, repository
  relocation, evidence, promotion, permission fixtures, snapshot rollback, and
  undo against disposable homes and repositories.
- [ ] **R7 - Run the full pre-tag gate.** `./scripts/gate.sh` must end with
  `ALL GATE CHECKS PASSED` on the exact release commit.
- [ ] **R8 - Publish one release.** Tag that commit as `v0.15.0`, push the tag,
  wait for the release workflow, verify all 18 archives plus `SHA256SUMS`, and
  perform the post-tag install smoke for `homonto`, `onto`, and `to`.

## Deliberate non-goals (v0.15.0)

- No automatic permission edits or wildcard generalization.
- No command execution through `onto evidence`; the agent runs verification
  under the host's normal permission checks.
- No byte-for-byte backup of complete OpenCode files.
- No restoration over user-modified managed values.
- No simultaneous onto and `to` framework installation during promotion.
- No identity or approval-role system in onto; Git remains the audit record.

---

# Development plan - v0.13.0 (delivered)

> Goals set by the maintainer on 2026-09-01, recorded here before work starts.
> Each item carries its decided shape; the deliberate non-goals are recorded
> too, so "why didn't X land" has an answer.

## 1. OpenCode becomes the only adapter

Claude Code support is **removed**, and the codex MCP pilot goes with it.
Any config naming a removed tool — `targets = ["claude"]` or
`targets = ["codex"]`, `[settings.claude]`, `[plugins.claude.*]`,
`[marketplaces.claude.*]`, or a `[subagents.<name>.claude]` block — fails
load loudly, naming the key and the release that removed it (same
fail-closed precedent as the v0.3.0 framework removal, ADR 0015).
Consequences that follow rather than get decided: the claude and codex
adapter packages, the conformance suite's cross-adapter matrix, and
`homonto import` (its only source was Claude's global MCP config) are all
deleted; the agentfm Claude render (aliases, `opus[1m]` variants, effort
levels) goes with them.

## 2. Model variants everywhere models are used (OpenCode)

`variant` and `effort` (medium, high, xhigh, …) become first-class wherever
a model is declarable today — `[settings.opencode]` and
`[subagents.<name>.opencode]` — rendered the way OpenCode spells them.
`ModelSpec` already carries all three neutrally; only the OpenCode render
and validation learn them.

## 3. Implementer skill: KISS, Unix philosophy, composition over inheritance

The onto and `to` implementer skills gain explicit build requirements:
adapt the KISS rules the frameworks already enforce, prefer Unix
philosophy (small, composable, do-one-thing pieces) where possible,
prefer composition over inheritance, and where OOP is genuinely the right
tool, follow SOLID. Catalog prose change; catalog + framework versions
move.

## 4. Parallelization focus

With one adapter the remaining serialization is fetches and files:
`remote:` subagent fetch+verify in parallel, skill/command/subagent link
projection in parallel, catalog materialization where the fingerprint
gates allow it. Write-capability rules (ADR 0020/0035) keep deciding what may
run concurrently; nothing that writes shared state gets parallelized.

## 5. Multi-repo: designated places, cross-repo effect

All homonto, onto, and `to` state and artifacts live in designated places
(one selected config repo), but operations can modify other declared repos
too. This changes the ownership and safety contracts (prune roots, apply
lock scope, drift semantics), so an ADR and design doc precede any code;
if the design lands mid-release the implementation ships when the design
says it ships, not with the tag.

**Status 2026-09-01:** [ADR 0024](adr/0024-multi-repo-designated-state-cross-repo-effect.md)
is implemented. `[repos]` is load-validated; repo-tagged project resources
project into the selected declared repo with isolated state; and `onto new` /
`to new` record selected `--repo` aliases for terminal Git gates. All state
and workflow artifacts remain in the config repo, selected repo dirt or an
unavailable worktree fails close/done, and unselected declared repos do not
block. The single-repo flow stays default and first-class.

## Deliberate non-goals (v0.13.0)

- No new adapters to replace the removed ones — OpenCode only, by decision.
- No migration path for Claude configs beyond the loud load error naming
  what to delete.
- Multi-repo does not become a monorepo mandate: single-repo use stays
  first-class and default.

---

# Development plan — v0.1.8 → v0.2.0 (delivered)

> **This plan is history.** Everything below shipped across releases v0.1.8 →
> v0.2.2; it is kept for the rationale behind each decision, not as a list of
> pending work. For where things stand now, read the next section first.

## Status after v0.4.0 (2026-07-18)

**Delivered.** The `to` framework shipped in v0.4.0: the third binary
(`cmd/to`, plan → do → done, JSON output on its workflow commands, git-blind,
gated bootstrap),
the `builtin:to` catalog framework (dispatcher + three phase skills,
`to-no-slop`, four subagents), the onto-xor-to config
exclusivity, and the `to-lifecycle` Docker E2E suite in the release gate.
Design record: [to-framework-design.md](to-framework-design.md).

### `to` fix list (post-v0.4.0)

Reviewed 2026-07-18. Scope rule for every item: `to` is lightweight **by
design** for repos that already run homonto — fixes below harden what shipped
without adding ceremony. The deliberate structural choices (self-asserted
`--verified`, one sequential skeptic, no escalation path, gated bootstrap) are
not on this list; they are the product.

**Status 2026-07-18: items 1–7 are fixed on main** (crash convergence,
date-prefixed archives with same-day suffixing, `to doctor --quiet`,
handoff excerpting head + unchecked tasks, the workspace lock, the
`to-reference` guide + doc index updates, and the optional `--evidence`
flag). Item 8 (live-skill exercise) remains deferred to the testing pass.

**P1 — defects.**

1. **`done`/`abandon` crash convergence.** Both save terminal state, then
   rename into `docs/tasks/archive/`. A crash (or an archive-name collision)
   between the two leaves a terminal change in the active tree that every
   command refuses to touch — wedged with no recovery command. Same defect
   class onto fixed in v0.2.1. Fix: re-running `done`/`abandon` on a
   terminal-but-active change completes the archive move (idempotent
   finish), and the collision check runs before the state write.
2. **Archive names are consumed forever.** `to new` refuses any name present
   in the archive and archive dirs are not timestamped, so a recurring chore
   name (`update-deps`) works once per repo. Fix: archive as
   `<date>-<name>` (onto's convention), free the name for reuse.

**P2 — robustness.**

3. **`to doctor [--quiet]`.** No health check exists, so there is no hook
   primitive — the [enforcement](guides/enforcement.md) recipes cannot apply
   to `to` at all. Minimal doctor: wedged terminal-active changes (item 1),
   state-file validity, `plan.md` present, plan-checkbox contract (the
   `to-do` resume logic depends on `- [ ]` lines the binary never checks),
   and binary↔materialized-framework version skew. `--quiet` = exit-code
   only, mirroring `onto doctor`.
4. **`handoff` truncates the wrong end of the plan.** The excerpt is the
   first 60 lines, but during `do` the unchecked tasks sit at the bottom —
   a long plan hands off its finished history and cuts its remaining work.
   Fix: excerpt = plan head (goal) + every unchecked `- [ ]` line.
5. **No mutating-command lock.** Two concurrent sessions can interleave
   `phase`/`done` on one change (writes are atomic; last-writer wins
   silently). Reuse the applylock pattern for `to`'s mutating commands.

**P3 — docs and decisions.**

6. **User-facing docs.** `to` is documented by its design record and the
   release notes only; `getting-started`, `cli-reference`, and
   `troubleshooting` cover the other two binaries. Add a `to` quickstart +
   CLI reference section, and mention `to` on the README start-here path.
7. **Decide: optional evidence string on `done --verified`.** An optional
   `--evidence "<text>"` recorded verbatim in the archived state would make
   real and skipped verification distinguishable after the fact, at zero
   added ceremony (it stays optional). Maintainer decision — the mandatory
   variant was considered and rejected in the design interview.
8. **Live-skill exercise (deferred by decision).** The mechanical layer is
   E2E-tested; the skills have not yet driven a real agent session
   end-to-end. Planned as part of the future testing pass, not scheduled
   here.

## Status after v0.3.0 (2026-07-15)

**Delivered.** The whole v0.1.8 → v0.2.0 plan below (releases v0.1.8–v0.1.15
and v0.2.0), plus: v0.2.1's deep-review fixes (onto's terminal states, the
content-fingerprint materialize gate, override validation), v0.2.2's
dirty-workspace support (`onto dirt`, classified dirt, the close carve-out for
other changes' in-flight docs), and v0.3.0's catalog narrowing — the bundled
catalog now ships only homonto-native content
([ADR 0015](adr/0015-ship-only-onto-frameworks.md)).

**Known open — not scheduled, each needs a maintainer decision:**

- **The `to` framework.** ~~A second native framework is planned; its scope
  and content are unspecified. Nothing is built.~~ **Shipped in v0.4.0** —
  see the status section above and its fix list.
- **A dedicated `[hooks]` resource** (v0.2.0 item 1's remainder). The feasible
  parts shipped — `onto doctor --quiet` plus the Claude `settings.json` and
  OpenCode plugin recipes in [enforcement](guides/enforcement.md). Auto-shipping
  onto's guard to both tools needs an OpenCode **JS plugin** (no declarative
  hook config exists) whose *execution* cannot be tested in this environment.
  Environment-gated, not undone.
- **Real-tool E2E in CI** (v0.2.0 item 2). `test/e2e/` drives actual Claude
  Code + OpenCode locally; wiring it into CI needs GitHub **secrets** for live
  models. The render invariants it asserts already run on every push through
  the Docker E2E (`homonto-expanded`).
- **Dogfooding onto in this repository.** Deferred to v1 by decision; this
  repo is developed directly on branches, with no external workflow stack
  (see [personas](personas.md)).



Written 2026-07-14 from three analyses: onto-vs-comet gap review, the
subagent/dialog/tool-parity method, and flow-correctness findings. One release
per section, ordered so each ships alone and each unblocks the next. The method
underneath everything: **declare intent once, tool-neutrally; render each tool's
native dialect at projection time; parity tiers are explicit; behavior that can
live in the `onto` binary lives there** (identical everywhere by construction).

Status legend: each item lists Goal / Changes / Acceptance. A release ships only
behind the full gate (unit + `-race` + E2E) like everything else.

---

## v0.1.8 — Flow correctness: task lists at the right time

**Problem.** `onto new` scaffolds `tasks.md` at birth and the open-exit gate
requires it — task decomposition before any design exists. onto-design then says
"update tasks.md if the design…" (draft-then-patch, backwards). Bonus defect
found while grounding this: presets (fix/tweak) can never mechanically reach
`close` — leaving `design` demands a `design.md` presets never write (the "N2
residual").

**Design.** Workflow-aware artifact gates:

| Leaving | full | fix / tweak |
|---|---|---|
| `open` | `proposal.md` | `proposal.md`, `tasks.md` (open-lite checklist is the right time) |
| `design` | + `design.md`, **`tasks.md`** (derived from the confirmed design) | *(pass-through: no design.md demanded)* |
| `build` | + `plan.md`, all tasks checked | all tasks checked (no plan.md) |
| `verify` | + `verification.md` | + `verification.md` |

Empty/unknown workflow = full (strictest, matches closeEvidenceGate).

**Changes.**
- `internal/ontostate/state.go`: `RequiredArtifacts(phase, workflow)`;
  `ValidateSkeleton` passes the loaded workflow.
- `internal/ontocli/advance.go`: pass `st.Workflow`.
- `internal/ontocli/new.go`: scaffold `tasks.md` only for presets; full scaffolds
  `proposal.md` only.
- Skills: onto-open stops drafting tasks (gate reviews proposal only);
  onto-design gains an explicit "derive tasks.md from the confirmed approach"
  step (template stays at `onto-open/references/tasks.md`, cross-referenced);
  dispatcher derivation row `proposal + tasks → design` drops the tasks conjunct.
- `test/docker/onto-lifecycle.sh`: create tasks at the design exit, not at new;
  add a preset leg that advances fix open→…→close mechanically (regression for
  the N2 fix).
- Catalog bump.

**Acceptance.** Full change cannot leave design without tasks.md; preset reaches
close via `onto advance`/`onto close` only; all suites green.

---

## v0.1.9 — Real subagent integration (neutral intent → capability-aware render)

**Problem.** v0.1.3–4 shipped enforced read-only agents but: no implementer
agent (`build-mode subagent` has nothing to dispatch to), the `coding`/`trivial`
model routes are dead config for agents, no delegation-topology enforcement, and
commands/agents can't use either tool's native powers (verified: OpenCode
`permission.task: deny` removes the task tool; project commands honor `agent:`).

**Design.** Extend the `homonto:` neutral block and render per tool at
materialize time **with config in hand**:

```yaml
homonto:
  role: coding        # → Claude `model: sonnet` / OpenCode `model: <route>` from [models.<tool>.coding]
  read_only: false    # existing
  bash: true          # existing
  dialogs: true       # existing
  spawn: []           # [] → Claude: omit Task from tools; OpenCode: task: deny  (full parity)
                      # [a,b] → OpenCode task globs (enforced); Claude advisory  (approximate)
  primary: true       # OpenCode: mode: primary + steps; Claude: SKIP render (entry stays /onto)
  steps: 60
```

Parity tiers are explicit; the renderer skips rather than mis-renders.

**Changes.**
- `internal/agentfm`: v2 schema + `RenderContext{Routes, AgentNames}`;
  `MaterializeSubagents` receives the context from the engine (which has Cfg).
- Catalog: new `onto-implementer` (role: coding, read_only: false, spawn: []),
  a new `onto` primary agent (OpenCode-only render; dispatcher prompt;
  `spawn: [onto-implementer, onto-explorer, onto-reviewer]`); explorer and
  reviewer gain `role:` tiers.
- Command rendering: generalize the per-tool variant mechanism to commands so
  `/onto` in OpenCode carries `agent: onto` (routes into the primary agent);
  Claude keeps its dialect untouched.
- Skills: onto-build's `build-mode subagent` path dispatches the implementer
  (spec handoff → diff back → reviewer pass); **subagents-never-prompt
  protocol** (they return a `Questions:` section; the orchestrator asks) — fixes
  the Claude asymmetry where Task subagents cannot prompt mid-run.
- Tests: conformance fixtures asserting both renders per intent + the semantic
  claims ("implementer cannot spawn" holds in both outputs); E2E asserts live
  invariants via `opencode debug agent` (edit/task/question) and the Claude
  variant's `tools:` line.

**Acceptance.** `onto set build-mode subagent` has a working target in both
tools; agent models differ by role per the user's routes; topology mechanical in
OpenCode and Task-omitted in Claude; all invariants in CI, not hand-checked.

---

## v0.1.10 — Gates as dialogs + discipline depth

**Problem A (gates).** Every `> **GATE:**` block is free prose — inconsistently
asked, silently skippable, answers recorded (if at all) in notes.md prose.

**Problem B (coding disciplines).** Comparing onto against the superpowers skill
set it absorbed: the absorption is 30–50:1 lossy exactly where discipline holds
under pressure — TDD (371 lines → ~6: the rationalization defenses are gone),
systematic debugging (296 → ~8: the phased method and 3-failed-hypotheses
escalation are gone), **receiving-code-review (213 → nothing — and load-bearing
since v0.1.3 piped the reviewer subagent's findings back to the orchestrator,
which now implements them unexamined)**, and worktree mechanics (202 → the
recorded choice with no how). Onto is *stronger* than superpowers on
verification (a gated phase), requesting review (an enforced read-only agent),
and subagent execution (a real protocol reference) — the gap is specifically the
four above. Structural cause: comet *composes* superpowers (loads the deep skill
at the moment of need); onto inlined summaries for self-containment.

**Design.** The binary owns the gate schema; skills only render it. For the
disciplines, use onto's own ADR 0006 reference-file mechanism — **vendor the
deep protocols as `references/*.md`** loaded on demand (the onto-no-slop /
subagent-protocol pattern), no dependency on superpowers, self-containment kept.

**Changes.**
- `onto gate <change> [--json]`: emits the pending gate — id, question, short
  header, options (with a recommended default), and which `onto set` records the
  answer. Pure read; derived from phase + state.
- Recorded answers become state (reuse existing setters; add
  `onto set decision <change> <gate-id> <choice>` for confirm-only gates).
- Skills: gates render through AskUserQuestion (Claude) / question tool
  (OpenCode) from the emitted schema; free-prose gate text shrinks to intent.
- Vendored discipline references (prose-only, one catalog bump):
  - `onto-build/references/receiving-review.md` — verify each reviewer-subagent
    finding against the code before implementing; evidence-based pushback; no
    performative agreement. **Highest priority: closes the loop v0.1.3 opened.**
  - `onto-build/references/tdd-protocol.md` — full red/green discipline,
    watch-it-fail-for-the-right-reason, never weaken a test, the rationalization
    table. (`tdd-mode: tdd` is onto-fix's mandatory default; its enforcement
    prose is currently ~6 lines.)
  - `onto-build/references/debugging-protocol.md` — phased method (reproduce →
    whole error → recent changes → data-flow → hypothesis → minimal experiment),
    shotgun fixes forbidden, escalate after 3 failed hypotheses.
  - `onto-build/references/worktree-protocol.md` — the mechanics behind
    `onto set isolation worktree` (creation, env/state copying, cleanup).
  - Enrich `onto-build/references/plan.md` with the writing-plans method (task
    granularity, exact paths, per-task verification).
  - The **brainstorm protocol** reference (clarify → 2–3 approaches →
    trade-offs → user pick, checkpointed) that onto-design walks before
    design.md — comet's "brainstorming cannot be skipped," kept self-contained.
- onto-fix/onto-tweak/onto-build inline sections point at the references instead
  of paraphrasing them; onto-close gains keep/discard options + worktree cleanup
  in its integration step (the finishing-a-development-branch remainder).

**Acceptance.** Same gate asks the same question with the same options in both
tools; every gate answer is inspectable in `onto state --json`; each vendored
protocol is reachable from the phase skill that needs it, and the inline
paraphrases are gone (single source per discipline).

---

## v0.1.11 — Measured gates trio (comet parity, small mechanical wins)

Three items the schema already anticipates; all "shape, not judgment" (B1):

1. **`onto scale <change>`** — derive the verification level from the measured
   `base_ref..HEAD` diff (files/lines; comet-state scale equivalent); prints the
   level and optionally records `verify-scale`.
2. **verify-round discipline** — `onto set verify-result fail` auto-increments
   `observed.verify_rounds` (today nothing writes it); `status`/`doctor` surface
   "N failed rounds" and the ≥3 rule ("user must choose accept-deviation or
   continue") becomes a named finding.
3. **`build_pause`** — a recorded plan-ready pause (`onto set build-pause
   plan-ready|null`) so a stopped session (or model switch) resumes cleanly;
   dispatcher resumes from it.

**Acceptance.** Scale output matches a fixture diff; a third failed verify is a
doctor finding; a paused change resumes at the pause point in a fresh session.

---

## v0.1.12 — Mechanical spec-delta merge

**Problem (top correctness hole).** onto-close's spec merge — RENAMED →
MODIFIED → REMOVED → ADDED application into living specs — is agent-performed
prose; the most destructive step in the workflow depends on model care. Comet
delegates the same step to a CLI.

**Changes.**
- `onto merge-deltas <change>` (also invoked by `onto close` when deltas exist):
  deterministic application of the four sections in order, duplicate-requirement
  and leaked-delta-heading lint post-merge, idempotent through an exact
  pre/post-image receipt bound to `close.merged`.
- onto-close step 3 shrinks to: assemble plan → confirm → run the command →
  review its report. Skill keeps ADR numbering (rename-scan guard) for now.
- Golden-file tests per section type + conflict cases (MODIFIED targeting a
  RENAMED name; ADDED duplicate = error).

**Acceptance.** A fixture change's deltas merge byte-identically to the golden
output; a doubled run is a no-op; the lint blocks a seeded duplicate.

---

## v0.2.0 — Enforcement layer + CI parity

1. **Hooks projection** — new neutral resource (`[hooks.*]` / framework-shipped)
   rendered per tool: Claude `settings.json` hooks (PreToolUse/Stop) and an
   OpenCode plugin shim reading the same manifest. onto ships phase-guard hooks
   (e.g. Stop → `onto doctor --quiet`) — comet's hook-guard, installed
   declaratively. This is the layer that makes gates non-skippable.
2. **Real-tool E2E in CI** — wire `test/e2e/` (dual-binary matrix driving actual
   Claude Code + OpenCode) into the gate; the parity invariants from v0.1.9
   run on every push, not by hand.
3. **`onto handoff <change> --write`** — hashed, compact context pack per phase
   boundary (comet's handoff package): compaction recovery gets content back,
   not just phase.

**Acceptance.** A denied gate is mechanically intercepted in both tools; CI
fails when either tool stops honoring a rendered invariant.

---

## Deliberately not planned

- Deterministic intent routing (CometIntentFrame) — dispatcher tables are
  simpler and sufficient.
- Artifact language config (en/zh-CN) — no current need.
- Binary self-update — `go install @tag` / release archives own that.
- Per-resource `review_mode` knob — folded into build-mode + reviewer agent.
