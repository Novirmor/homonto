# Release notes intro

This file is prepended to every GitHub release's auto-generated notes by the
`release` workflow (`--notes-file docs/release-notes.md --generate-notes`), so
every release states the accepted limitations up front. Keep it short; the
per-release changelog is generated automatically below it.

---

## What's in this release

This release ships **three binaries** — `homonto` (config projector), `onto`
(spec-driven workflow operator), and `to` (minimal coding-framework
bookkeeper) — for every supported OS/arch as separate archives under one
`SHA256SUMS`. `onto` and `to` each require `homonto` to have installed their
framework first (`[frameworks.onto]` / `[frameworks.to]` + `homonto apply`).

### New in v0.27.0 — cleaner setup and GitHub skill naming

- `homonto init` now creates only `homonto.toml`, `.gitignore`, and
  `.env.example`. It no longer creates an unused `homonto/skills/.gitkeep`.
  Existing local content is preserved; create and declare local skills when
  needed.
- The installer, catalog, and current guides describe `h` as a **GitHub skill
  bundle**, not a third lifecycle workflow. `[frameworks.h]`, the five `/h-*`
  entry points, and its dependencies on onto and to are unchanged.
- Guided setup rejects invalid repository selections, numeric-only repository
  names, model `#variant` suffixes, workflow-root aliases of the config root,
  and records/tmp collisions. The config loader and workflow resolver also
  reject root aliases such as `./` and `docs/..`.
- Installation refuses directory targets for executables. Retry loops stop
  cleanly at EOF, invalid detected model defaults can be replaced, and path
  validation ignores inherited `CDPATH`. Empty-array handling and version
  validation work with Bash 3.2 as well as current Bash.
- Next steps reflect selected configuration and installed binaries. The model
  prompt discloses its global OpenCode scope. Current documentation corrects
  coordinator naming, local-skill setup, repository relocation, and the limits
  of workflow diagnostics.

### Upgrading to v0.27.0

Install all three binaries at v0.27.0, then run `homonto update` (or
`homonto apply`) in each configured project and restart OpenCode. Existing
local-skill directories are not deleted, and workflow records are not migrated.
If an old `workflow.root` resolves to the config directory itself, choose a
dedicated records directory before applying; these aliases are now rejected.

### New in v0.26.0 — autonomous coordinator permissions

- The dedicated `/onto-bypass` and `/to-bypass` commands now prompt the
  coordinator for confirmation instead of being unconditionally denied. They
  remain unavailable to implementers and undiscoverable to ordinary workflow
  skills.
- The coordinator can perform all local Git operations without a tool prompt;
  `git push` still asks. Implementers retain destructive-command prompts.
- When `[tooling] shell_proxy = "rtk"`, the coordinator receives an explicit
  `rtk *` permission. Recognized publication, workflow-bypass, and destructive
  non-Git commands still prompt. See [ADR 0055](adr/0055-require-confirmation-for-explicit-workflow-bypasses.md),
  [ADR 0056](adr/0056-allow-coordinator-local-git-operations.md), and
  [ADR 0057](adr/0057-allow-coordinator-rtk-commands.md).

### Upgrading to v0.26.0

Install all three binaries at v0.26.0, then run `homonto update` (or `homonto
apply`) and restart OpenCode. Reapplying updates the coordinator's rendered
permission map; no workflow records migrate.

### New in v0.25.0 — trusted workspace shell by default

- The coordinator and implementers now default to trusted-workspace shell
  execution, including inspection, setup, cloning, Python/Node and repository
  scripts, chains, and pipes. This deliberately overrides inherited Bash
  asks/denies; it does not change edit behavior, directory grants, delegation,
  or task write scope. Reviewers and other read-only specialists still deny
  shell and edits.
- Neutral agent profiles gain `bash_default: allow|ask` and `bash_ask`.
  `allow` omits generic composition asks; omitted baselines retain guarded
  custom-profile behavior. Known `git push`, GitHub publication, and raw `gh api`
  patterns ask for the coordinator but are denied for implementers. Destructive
  patterns ask for both writable roles. Protected asks/denies follow exact
  additions and cannot be overridden by `bash_allow_add`.
  Strict users can use guarded custom agent definitions; there is no new
  model-route configuration knob. See [subagents](guides/subagents.md).
- Implementers may perform task-authorized Git/gh setup and reads within their
  write scope. The coordinator still owns authoritative GitHub intake, workflow
  state, integration, and publication. Default-allow shell does not authorize
  publishing: invocation scope, verification, and approval of the shown review
  draft still bind operations inside scripts. Past accepted commands are not
  publication consent; a tool prompt cannot override role ownership or approval.
- Onto/to preflight investigates missing, broken, or incompatible binaries before
  handing off: inspect PATH and known compatible installations, then repair only
  already-authorized setup from a trusted source/version into a workspace-local
  destination. No silent global-binary overwrite, shell-profile edit, or arbitrary
  untrusted installation. Unresolved failures name the setup blocker or decision;
  workflow mutations wait for version checks and the framework-install gate.
- Registered worktree lifecycle and dirty-work decisions remain required.
  Isolated, authorized task-local Git fixtures cannot replace live bindings or
  recreate the control plane. Finite command patterns cannot cover every risky
  tool, sandbox scripts/wrappers, or contain arbitrary shell directory access;
  code runs with the process's files, credentials, and network privileges.
  This accepted tradeoff replaces the finite default allowlist in v0.24.0, not
  workflow authorization. See [ADR 0054](adr/0054-default-to-trusted-workspace-shell.md).

### Upgrading to v0.25.0

Install all three binaries at v0.25.0, run `homonto update` (or `homonto apply`)
in each configured project, and restart OpenCode to load the new agent profiles.

**Reapplying existing onto/to/h installations enables the loose shell default,
including schema-0/1 configurations.** Schema 2 is not required for this policy
change. The coordinator and implementers can execute arbitrary task-scoped
commands, scripts, and shell composition without a generic approval prompt.
Their Bash baseline overrides inherited Bash asks and denies; finite protected
exceptions are not a sandbox. If you need the former guarded policy, select
guarded custom agent definitions before reapplying. Custom profiles that omit
`bash_default` retain their existing behavior.

Read-only specialists remain shell- and edit-denied. Publication authorization,
explicit review-draft approval, workflow-state ownership, and dirty-work cleanup
decisions remain required. No workflow records are migrated by this upgrade.

### New in v0.24.0 — separate workspaces and autonomous workflows

- Schema 2 separates config, records history, and explicit source scope, with
  registered execution and identity-checked terminal/archive receiver worktrees
  (`--state-id` disambiguates, never rebinds). Managed operations journal scoped
  pre-write intent with a prepared archive destination/date and recover partial
  records without sweeping unrelated paths; same-file editor attribution and
  automatic layout/active-binding conversion remain unsupported. See
  [workspaces](guides/workspaces.md).
- Onto retains superseded evidence as history, requires unique scenario
  declarations (including no-spec presets), and reports failed-round history
  only while unresolved. Ambiguous IDs provide no coverage or supersession.
  See [ADR 0053](adr/0053-keep-history-without-blocking-fresh-verification.md).
  Integration binds exact verified candidates (plus allowed records-only descendants), accepts
  proven `unchanged:` receipts, and requires an externally asserted PR `--head`
  for explicit sources without claiming remote attestation. To surfaces root
  errors and preserves actual verification values during archive recovery.
  Demotion preserves task scope and multiline checks, transfers deferred-close
  work unchecked, and restarts incomplete contracts at plan.
- Strict config diagnostics reject ignored fields and duplicate builtin aliases;
  per-agent positive `steps` overrides tune finite budgets. Permission suggestions
  use the actual `permission.asked`/`permission.replied` producer contract. The
  workflow observer refreshes compaction status and reports transient errors;
  notifications are not enforcement.
- The five h workflows now work toward explicit completion goals, choose `to`
  or `onto` from intent and evidence, and recover in-scope failures without
  routine approval dialogs. Review drafts still need approval before posting.
- The coordinator and implementers allow routine tests, builds, and formatting,
  including on contributor-controlled PR checkouts. All shipped agents allow
  web research; concurrent specialists remain shell- and edit-denied.
- This deliberately trusts repository scripts to execute arbitrary code with
  the process's privileges. Unknown command requests still ask; publishing
  prompts and explicit denies remain. Composition guards apply to the host's
  permission-request patterns, not necessarily whole shell invocations.
  Configured RTK gets command-specific wrapper rules, not a blanket grant.
  Permission denial cannot be bypassed. This replaces the per-run PR execution policy described below
  for v0.23.0. See [ADR 0051](adr/0051-trust-workspace-execution-and-automate-h-routing.md).
- The installer now passes an explicit stdin operand to checksum verification,
  fixing false checksum mismatches with macOS `sha256sum` ([#7](https://github.com/Novirmor/homonto/issues/7)).

### Upgrading to v0.24.0

Install all three binaries at the same version, then run `homonto update` (or
`homonto apply`) in each configured project. Restart OpenCode to reload the
updated plugins, agent permissions, and budgets.

- **Schema 2 is opt-in.** Configurations using schema 0/1 retain their implicit
  configuration-repository scope. Do not change schema or workflow-root ownership
  as a shortcut for migration: existing records are not moved or adopted
  automatically. See the [workspace guide](guides/workspaces.md).
- **Configuration is stricter.** Previously ignored keys, misplaced tuning
  fields, and multiple aliases for one builtin agent now fail with diagnostics.
  Correct the named declaration rather than editing generated files.
- **Execution trust changes.** Routine tests, builds, and repository scripts,
  including checked-out PR code, can run without individual approval. They run
  with the process's privileges, not in a sandbox. Review publication still
  requires approval of the shown draft, and tool-level publication prompts remain.

### New in v0.23.0 — executable workflow contracts and safer agent projection

- **Workflow agents can complete their documented gates.** The shared
  coordinator records routine proposal, approach, verification, and close
  evidence while explicit `onto` and `to` bypasses remain unreachable. Preset
  verification now shares onto's risk classifier, so a small change to a
  security-sensitive surface receives full verification. Close recovery
  distinguishes completed spec merging from ADR promotion, and local
  integration pins the post-archive commit so the base branch receives the
  archive as well as the verified source.
- **PR continuation keeps its execution boundary across delegation.**
  Implementers prompt for shell execution, deny workflow and publishing
  commands, and stop PR continuation when inherited auto-allows would bypass
  per-command approval. Inline review comments now carry author association
  through every paginated query; only owner, member, and collaborator feedback
  routes automatically.
- **Projection rejects malformed or unsafe input before publishing it.** Model
  variants and additive Bash permissions reject multiline, wrapper, shell,
  destructive, privilege-escalating, and network-fetching forms consistently
  in both configuration and permission suggestions. Invalid or unknown
  `homonto:` capabilities fail closed, and subagents are preflighted before a
  catalog update publishes any content.
- **The guided installer rejects non-model answers to its model question.**
  This fixes a validation gap introduced after v0.22.0.

### New in v0.22.0 — a declared workspace tmp directory

- **`[tmp]`: one scratch directory the whole projection shares**
  ([ADR 0048](adr/0048-one-declared-workspace-tmp-directory.md)). Declare
  `[tmp]` (optionally `dir`, default `.tmp`) and every apply creates the
  directory, keeps it gitignored, and generates `references/tmp.md` into the
  framework dispatchers and the shared `homonto` skill naming the path and the
  contract. Because it sits inside the workspace, every writable agent —
  including custom agents — writes there with no prompts and no
  permission-map changes; read-only workers stay read-only and hand scratch
  to the coordinator. homonto never deletes tmp content: disabling the table
  withdraws the generated references but leaves the directory and its files
  untouched. The guided installer offers the declaration for new projects,
  and the path is fingerprinted with the materialize gate so a `dir` edit
  re-projects. The dir is validated against every tree homonto and the
  workflows own — the workflow records root, `.git`, `.opencode/`,
  `homonto/`, and the managed `.homonto` subtrees are rejected (equal, above,
  or inside), because a colliding scratch dir would be gitignored or rebuilt
  over. The h skills route their transient files (draft bodies, comment
  files, PR bodies) there too.

### New in v0.21.0 — one coordinator, and the h GitHub workflows

- **One `homonto` coordinator agent.** The separate `onto` and `to` primary
  agents are replaced by a single shared `homonto` primary; `/onto`, `/to`,
  and every `/h-*` command route into it, and it loads whichever dispatcher
  the change needs
  ([ADR 0045](adr/0045-one-homonto-coordinator-for-both-workflows.md)).
  **Breaking:** rename any `[subagents.onto.opencode]` or
  `[subagents.to.opencode]` model block to `[subagents.homonto.opencode]` —
  exactly one block; the old names fail at load naming the fix. The
  installer writes the new shape and can add the `h` bundle to new projects.
- **The `h` framework: GitHub intake over both workflows.** Declaring
  `[frameworks.h]` (it depends on onto and to, and an applied `h` satisfies
  both binaries' install gates) adds five workflows:
  `/h-spike-issue` (read-only issue research through the `h-spike` worker),
  `/h-resolve-issue` (spike, an explicit to-or-onto choice, then the chosen
  workflow drives to a verified pull request), `/h-review-pr` and
  `/h-review-batch` (full-context review through read-only `h-review`
  workers — drafts first, nothing posted without an explicit approval of the
  shown draft), and `/h-continue-pr` (collect outstanding feedback, resume
   or open the matching workflow change, merge verified onto work into the
   existing PR head, push only verified fixes, comment with evidence). Only the coordinator talks to GitHub; workers receive
   prepared context and return analysis. The coordinator itself denies
   open-web fetch/search (GitHub access flows through the approved `gh`
   surface), and commands that execute repository-controlled code — test
   runners, package-manager scripts, make targets — always prompt, so a
   checked-out PR head cannot run contributor scripts silently. Compound
   shell commands always prompt too: an allowlist entry no longer matches a
   chained second command, and the workflow's own gate-skipping subcommands
   (`onto`/`to bypass`, evidence-token writes) are denied outright
   ([ADR 0047](adr/0047-deny-open-web-and-compound-shell-around-github-intake.md));
   MCP servers a config enables stay outside this map as the owner's
   trust decision.

### New in v0.20.0 — guided project configuration

- **The installer configures new projects.** After `homonto init` creates a
  config, the installer asks for the workflow-record directory, existing
  sibling Git repositories, `onto` and/or `to`, and one OpenCode model for the
  main session plus all selected framework agents. Existing `homonto.toml`
  files remain untouched ([ADR 0044](adr/0044-the-installer-configures-new-projects.md)).
- **Terminal UI selection.** Interactive installs prefer Gum, then dialog, and
  use plain text when neither dependency is available. `HOMONTO_UI=dialog`
  selects dialog explicitly.

### New in v0.19.0 — complementary workflows and a guided install

- **onto and to are complementary.** A repository may declare both
  `[frameworks.onto]` and `[frameworks.to]`: records stay in disjoint
  directories, agents and commands are namespaced, and `/onto` or `/to` picks
  the workflow per change. Active change names are globally
  unique across both trees, and `to status --all` reports one combined
  inventory ([ADR 0042](adr/0042-onto-and-to-are-complementary.md)).
- **Reversible promote/demote with a shared conversion engine.** `onto demote
  <name> --yes` joins `to promote`: sources move whole into a neutral
  `.workflow/` control plane (lineage, receipts, snapshots) instead of
  nested `imported-*` copies; demotion is phase-aware (open/design → plan,
  build/verify → do with a translated doctor-clean plan, terminal states
  refused); completion is receipt-verified, staging authenticates before
  generating, and converting back while unchanged restores the previous
  workspace byte-for-byte. Every onto mutator now takes the onto workspace
  lock, in one fixed order with the bridges' locks.
- **The installer guides setup.** Prompts use
  [gum](https://github.com/charmbracelet/gum) when available and stdin is a
  TTY (plain reads otherwise), the workflow-binary default is both, and on
  explicit confirmation it runs `homonto init` in the current directory
  ([ADR 0043](adr/0043-the-installer-offers-initialization.md)).

### New in v0.18.0 — interactive release installer and a configurable workflow root

- **Interactive release installer.** `scripts/install.sh` (Linux/macOS,
  amd64/arm64) asks which binaries you want, verifies the release archives
  against `SHA256SUMS`, installs into a directory you choose, and prints PATH
  instructions — it never edits your shell configuration and never runs
  project commands. Download-then-run, never `curl | bash`
  ([ADR 0041](adr/0041-ship-an-interactive-release-installer.md)).
- **Configurable workflow root.** `[workflow] root = "<relative path>"`
  relocates both frameworks' records below `docs/`; changing the root while
  workflow state exists fails closed
  ([ADR 0040](adr/0040-configure-one-workflow-root.md)).

### New in v0.17.0 — autonomous workflows and valid OpenCode resources

- **Workflows run through by default.** Starting or resuming `onto` or `to`
  continues through later phases unless the user names an endpoint or asks to
  pause. Primaries investigate before asking, choose reversible technical
  defaults, and reserve user questions for product intent and explicit waivers.
- **Every workflow command enters its primary agent.** Direct phase and preset
  commands now carry the same orchestration, permission, and delegation policy
  as `/onto` and `/to`.
- **OpenCode metadata and read-only agents are honest.** Shipped skills have
  discoverable metadata, unsupported command fields are gone, bypasses are
  command-only, and concurrent read-only specialists deny both edits and Bash.
- **Close validates and receipts every destructive boundary.** The verification
  report must contain one canonical result that agrees with state, and a
  recorded pass freezes each scoped repository's HEAD — source commits after
  the pass refuse close until the change is re-verified. Spec merges record
  exact delta and living-spec pre/post-images (a receipt from an invalidated
  round is discarded and recomputed, never a dead end), interrupted archives
  are executable to recover, and post-archive Git integration is tracked per
  repository with fail-closed receipts: a merge receipt must name a real
  `--no-ff` merge commit reachable from the recorded base branch, and a
  cross-repo change is done only when every selected repository landed.
  Integration targets a separately recorded branch rather than the
  commit-valued diff base; reused change names resolve to the newest archive
  generation deterministically.

### New in v0.16.0 — parallel workflow agents and explicit bypasses

- **Choose a workflow by selecting its primary agent.** `onto` stays the
  primary evidence-gated workflow agent. `to` now ships an equivalent primary
  agent and `/to` routes into it. Both frameworks install the shared `homonto`
   knowledge skill for configuration, projection, and workflow-choice guidance.
- **Audited emergency bypasses.** Dedicated `/onto-bypass` and `/to-bypass`
  entry points invoke direct, reason-required binary commands after an explicit
  user request.
  Each bypass writes a versioned sidecar with its command, target, timestamp,
  reason, and skipped gates; archive bypasses preserve unmerged workspace
  content rather than pretending it completed normally.

### New in v0.15.0 — one release, seven capabilities

- **Replayable handoffs.** `onto handoff --json`/`--write` and `to handoff
  --json`/`--write` emit versioned recovery packs: identity, derived phase,
  gates, commits, artifact digests, and a safe next argv. Persisted packs are
  metadata only — never artifact prose or free-form state.
- **`homonto explain`.** Every managed resource names its origin (direct or
  framework), destination, last operation, and removal record. State schema 3
  records provenance and a bounded tombstone ring; legacy state loads with
  unknown provenance, never guessed facts.
- **Portable projections.** Project-scope links carry relative targets, so a
  repository rename converges on the next apply; user and cross-repo links
  stay absolute. Stale links stranded by a wholesale move repair through the
  plan path only, exactly matching their records.
- **Requirement-to-evidence traceability.** Delta specs carry stable
  Requirement-ID/Scenario-ID markers; `onto evidence record` stores hashes
  only (never argv or output) and `onto trace` renders the typed graph.
  `onto doctor` flags unknown scenarios, duplicate IDs, unreachable commits,
  and stale verification artifacts.
- **`to promote`.** A growing `to` change converts into a full onto change
  at phase open with its complete workspace preserved under
  `imported-to/` — atomic, crash-recoverable, tamper-refused. Frameworks stay
  exclusive.
- **Permission suggestions.** The `permission-observer` plugin ships as owned
  catalog content; it correlates OpenCode's authoritative ask/reply events
  (pinned revision 50efc055), suggests the second explicit approval exactly
  once, and forgets it. `homonto permissions suggest` validates and renders
  `bash_allow_add` snippets; nothing is persisted or auto-applied.
- **Opt-in snapshots and undo.** `homonto apply --snapshot` journals semantic
  checkpoints; failures roll back, `homonto snapshot recover` finishes
  interrupted applies under an OS-released process lock, and `homonto
  snapshot undo` reverses a committed apply — refusing over user edits.
  Plain apply keeps its exact prior behavior.
- **State migration.** One state schema migration (2 → 3) adds provenance;
  a moved repository needs no manual link surgery. Run `homonto apply` once
  to converge.

### New in v0.14.0 — OpenCode agent variants and longer onto runs

- **Agent variants use OpenCode's native fields.** `model` and `variant` now
  render separately. homonto rejects `#variant` model IDs and the unsupported
  global `model_variant` setting instead of generating an unknown model ID.
- **The onto orchestrator gets a 1,200-step budget.** Its permissive Bash
  allowlist covers workflow commands, local Git work, and common Go, Node, and
  Make test commands. Remote and destructive operations still require approval.

### New in v0.13.0 — OpenCode-only and designated multi-repo work

- **OpenCode is the sole adapter.** Claude Code and codex configuration now
  fail closed with a migration error instead of projecting stale support.
- **Declared repos are real effect targets.** Repo-tagged project resources
  project into the named declared repository with isolated state and
  per-repository drift reporting; undeclared siblings remain untouched.
- **Scoped workflow gates.** `onto new` and `to new` accept `--repo` aliases.
  Workflow state stays in the config repo, while selected repositories must be
  Git-clean before `onto` close or `to` done can complete.

### New in v0.12.0 — a self-healing workspace lock, honest release notes

A maintenance release over v0.11.0: one user-visible behavior change, two
public-docs corrections (one of which shipped inside every release's notes),
and internal cleanups. Catalog content is unchanged (catalog 0.11.0, onto
0.8.0, to 0.6.0).

- **`to` reclaims a stale workspace lock by itself.** A SIGKILLed command
  used to wedge every mutating `to` command until `docs/tasks/.to.lock` was
  removed by hand. The lock already records its holder's pid; on a conflict
  `to` now probes it, and a pid that provably no longer runs (finished, or
  no such process) means the lock is stale — it is removed and the command
  proceeds. A live pid, a pid owned by another user, and a lockfile with no
  readable pid (a crash between creating the file and writing it) are never
  touched, so a live session's lock cannot be stolen. `homonto apply`'s
  project lock keeps its deliberately manual reclamation.
- **The standing limitations block no longer denies remote frameworks.**
  Every GitHub release carried the claim that remote sources exist for
  subagents only and there are no remote framework sources;
  digest-pinned `remote:` frameworks have been supported and documented all
  along. The block now states the real rule.
- **Release packaging fixes.** `scripts/build-release.sh` falls back to
  `shasum -a 256` when `sha256sum` is absent (macOS runners), keeping
  `SHA256SUMS` byte-identical; and the release checklist's post-tag smoke
  sequence now runs as written (it previously called `onto doctor` in a
  workspace where `onto init` had never been able to run).
- **Internals.** The dead `internal/merge` package (zero importers since the
  projector split) is deleted; the Claude and OpenCode adapters' duplicated
  key/document helpers have one owner in `baseadapter`, which also gains its
  first direct tests; and `config`'s ~180-line `validate` cascade is split
  into per-section helpers with unchanged rules — with one nondeterminism
  removed: skills now validate before commands instead of map iteration
  order deciding which error surfaces.

### New in v0.11.0 — a resumable record, enforced

onto's axis is now stated plainly: a change survives being handed to someone
who was not there. `to` matches onto's code standards and skips its record;
company size was never the distinction and is no longer claimed
([ADR 0021](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0021-onto-is-for-handoff-and-audit.md),
catalog 0.11.0, onto 0.8.0, to 0.6.0).

- **`onto doctor` now reports `tasks.md` ↔ `plan.md` drift** — a task number
  in one file and not the other, or any checkbox in `plan.md`. That pairing is
  the resume mechanism (continue at the first unchecked item, read its detail
  under the matching `## Task N.M`), and it was previously checked only by a
  prose item in the close-phase lint. **A workspace carrying existing drift
  will start failing `onto doctor`, including the `--quiet` enforcement hook.**
  That is the unreported problem surfacing, not a regression. A change with no
  `plan.md` is a preset and reports nothing.
- **Subagent concurrency follows write-scope, not framework**
  ([ADR 0019](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0019-parallelism-follows-write-scope.md),
  [ADR 0020](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0020-onto-parallel-implementers-are-supported.md)).
  `to` previously forbade all subagent parallelism; the justification only ever
  covered agents that *write*, and three of its four never do. Read-only agents
  now run concurrently in both frameworks; the single implementer stays serial
  in `to`, and in onto may run in parallel given a worktree each.
- **Verify's skeptic lenses were renamed** to `abuse`, `data-migration`, and
  `compatibility`. They previously collided with build's reviewer lenses on
  "security" and "contract" despite asking a different question — a reviewer
  reads the diff, a skeptic attacks the running system. Prose-only; nothing in
  the binary keys on lens names.
- **Docs corrected against the shipped binaries.** An audit found and fixed
  nine mismatches, including: the state field is `build_mode` (not
  `execution`); `read_only` renders as Claude's `disallowedTools:` **denylist**
  (not a `tools:` allowlist); the three evidence tokens
  (`proposal_approved`, `approach_confirmed`, `close_confirmed`) were absent
  from both the canonical schema and the command reference; `onto advance
  --to build` was undocumented; onto ships **five** agents, not four (the
  `onto` orchestrator renders for OpenCode only); and `schema_version` is a
  real top-level config key that rejects a newer config fail-closed.
- **Artifact sections have one owner.** Grounding is recorded in `proposal.md`
  (open) and `design.md` (design) and no longer duplicated into `notes.md`;
  scope `Non-Goals` belong to `proposal.md` alone.

### New in v0.10.0 — tooling providers are declared, not shipped

*(Prepared but never tagged; it ships as part of v0.11.0.)*

The `onto` and `to` frameworks no longer name `rtk` and `graphify` in their
shipped prose. Which tools the workflow grounds against is now configuration
([ADR 0022](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0022-tooling-providers-are-declared-not-shipped.md),
catalog 0.10.0, onto 0.7.0, to 0.5.0):

```toml
[tooling]
shell_proxy = "rtk"       # "rtk" | "none"
code_intel  = "graphify"  # "graphify" | "okf" | "none"
```

- **BREAKING for the rendered workflow — both keys default to `none`.** A
  config with no `[tooling]` table now gets a preflight that names no tool at
  all, and grounding falls back to direct file reading (already the documented
  fallback). **Existing configs keep loading unchanged**; only the rendered
  skill text differs. To keep the previous behavior, add the two lines above.
- **`okf` is a selectable code-intelligence provider**
  ([okf-generator](https://github.com/UmairBaig8/okf-generator)). homonto
  **references** it and never downloads, installs, updates, or executes it —
  exactly as it never installed `rtk` or `graphify`. Installing a provider
  stays your job.
- **`homonto apply` generates `references/tooling.md`** inside each framework's
  dispatcher skill, describing exactly the declared pair. Shipped `SKILL.md`
  files defer to it, so a provider you did not declare is never mentioned. The
  file is regenerated on every apply — do not hand-edit it.
- **An unknown key or provider name fails at load**, naming the offender and
  the accepted set (`tooling.code_intel "ctags" is not a known provider
  (accepted: graphify, okf, none)`).
- **`homonto doctor` warns when a declared provider is not detected.** It
  probes `PATH` and index/bundle directories only and never runs the provider,
  so it cannot hang. The finding never fails a projection.

Editing `[tooling]` re-renders on the next apply: the materialize fingerprint
gained a tooling component, so a provider change is no longer invisible to the
"everything up to date" gate.

### New in v0.9.0 — onto's judgment gates are enforced, not documented

`onto` gated artifacts and bookkeeping but took a change's word on the three
decisions that need human judgment, and on whether its verify actually passed.
Those are now enforced in the binary (capability spec
`openspec/specs/onto-evidence-gates/`, onto framework 0.6.0):

- **BREAKING for in-flight `workflow: full` changes — three evidence tokens
  gate the judgment decisions.** `onto advance` refuses to leave `open` without
  `proposal_approved` and to enter `build` without `approach_confirmed`;
  `onto merge-deltas` and `onto close` refuse for **every** workflow without
  `close_confirmed`, checked before any shared file is mutated. Record them
  with `onto set proposal-approved|approach-confirmed|close-confirmed <change>
  "<evidence>"` (free-form, convention `YYYY-MM-DD <summary>`). A change
  mid-flight when you upgrade blocks on its next `advance` until the token is
  recorded — the unanswered gate is simply re-asked. Preset workflows (`fix`,
  `tweak`) are exempt from the two design-phase tokens. Unanswered tokens show
  up in `onto gate --json`.
- **Leaving `verify` cross-checks the report against the state.** The first
  `Result:` line of `verification.md` must agree with `verify.result=pass`, so
  a recorded pass can no longer be self-asserted. `Result: pass (2 accepted
  deviations)` still passes; `Result: passing` does not.
- **`onto state <change> --json` now derives the working phase from workspace
  artifacts** (`derived_phase`, plus `phase_mismatch` when the state file's
  claim disagrees) instead of echoing the claim it is named to distrust.
  `onto status` annotates a disagreeing row with `(working: <phase>)`, and
  `onto state`'s text output does the same. The evidence table lives in tested
  Go rather than being re-run from prose per dispatch.
- **`onto advance <change> --to build` walks a preset from `open` to `build` in
  one call**, running every per-hop gate. It refuses for `workflow: full`
  (those advance one gate at a time), for any target other than `build`, and
  for a change already at or past `build`.
- The onto skills record their token at the gate they own, so an agent
  following the workflow gets this for free; `docs/guides/onto-workflow.md`
  carries the token table and the migration note. Catalog 0.9.0.

### New in v0.8.0 — explicit per-agent models; model tiers removed (BREAKING)

The model tier system is gone (ADR 0016). Model selection no longer routes
through `role:` frontmatter and shared `[models.<tool>.<tier>]` blocks —
every declared subagent names its own model, per tool, where the agent is
configured:

```toml
[subagents.onto-reviewer.claude]
model = "opus"
variant = "1m"
effort = "high"
```

- **BREAKING — `[models.<tool>.<tier>]` blocks are rejected at load**, naming
  the offending table. Delete them and declare a
  `[subagents.<name>.<tool>]` block (non-empty `model`) for every declared
  subagent × targeted tool — framework-expanded agents included. A missing
  block fails at load with `subagents.<name>.<tool> model is required`, so
  the offender is named instead of today's anonymous missing-tier error.
- **BREAKING — homonto no longer manages the main session model.** The
  route-derived default (`model` in Claude settings, `model`/`small_model`
  in OpenCode) is not projected anymore; each tool uses its own default.
  If you pinned the main model through `[models.claude.architectural]`,
  move it to `[settings.claude].model` (or `[settings.opencode].model` /
  `.small_model`) — the explicit settings path is unchanged.
- Catalog subagents no longer carry `role:` frontmatter; a leftover `role:`
  in your own agent files is ignored as unknown frontmatter.
- Tool variants of a subagent now materialize only for the tools it actually
  targets; stale variants of untargeted tools are removed on apply.
- `onto new` proposals now require a Non-Goals section (onto framework).

### New in v0.7.0 — security hardening + deep code-review pass

A full code-quality review of `internal/` found and fixed five HIGH-severity
silent-failure paths, eight maintainability hotspots, and several test gaps.
Shipped happy-path behavior is unchanged; every refactor was verified by the
existing test suite plus 92 new tests (871 → 963). The changes that **are**
user-visible all turn previously-silent bugs into loud errors.

**Trust boundary and exec hygiene:**
- **`git://` is rejected as a remote transport** (insecure, like `http://`
  already was). Use `git+https://`, `git+file://`, `https://`, or `file://`.
- **Every external `exec` (pass, git) is now bounded by a 30s timeout.** A
  hung gpg-agent passphrase prompt or a git credential prompt previously hung
  the whole CLI indefinitely; you can now Ctrl-C through it.
- **`context.Context` is threaded through `engine.Build`/`Apply`**, so a hung
  remote fetch is interruptible from the calling CLI.

**Loud errors where silence was a bug:**
- **A malformed `homonto:` frontmatter block now fails the projection** with
  a named parse error. Previously it was treated as "no block" and the agent
  was silently projected with no model line and default permissions.
- **A corrupted TOML tool file now fails the projection** rather than being
  folded into "key absent" — the previous behavior could emit a misleading
  "create" plan or report false drift.

**Maintainability (no behavior change):**
- New `internal/adapter/baseadapter` absorbs ~590 LOC of byte-identical
  methods between the Claude and OpenCode adapters; both adapters shrink by
  ~294 LOC each.
- New `internal/resourcepath` unifies the three former
  `skillpath`/`commandpath`/`subagentpath` packages (their switch bodies had
  drifted in subtle ways).
- New `internal/workcli` extracts the gate / `validChangeName` /
  `ErrQuietFindings` scaffolding shared between `ontocli` and `tocli`; the
  `"0.1.0-dev"` literal now lives in one place (`buildinfo.DevVersion`).
- The 1381-line `internal/config/config.go` god file is split into four
  focused files (`config.go` types / `load.go` decode+migrate+Load /
  `validate.go` validation / `expand.go` framework expansion).
- Three near-identical `doctor{Skills,Commands,Subagents}` methods collapse
  into one `doctorResource(tool, doctorOp)`.
- `internal/schema.ErrTooNew` is the shared sentinel for the five
  schema-version-too-new checks (state, config, onto-state, catalog builtin,
  catalog local) — callers can `errors.Is(err, schema.ErrTooNew)` instead of
  substring-matching.
- Error wrapping at six sites uses `%w` (was `%v`) so error types survive
  the engine boundary; catalog loader surfaces `fs.ReadDir` errors instead
  of treating an unreadable directory as missing.

### New in v0.6.1 — lossless per-tool agent rendering

An audit of the rendered agents against both tools' real contracts found
and fixed four silent information losses (catalog 0.6.0, onto 0.4.1,
to 0.3.1):

- **Claude renders a denylist, not an allowlist.** The old `tools:`
  allowlist silently stripped every unlisted default (WebFetch, WebSearch,
  Skill, …) that the OpenCode variant kept. Claude now gets
  `disallowedTools:` covering exactly the denied intent — read-only denies
  `Edit, Write, NotebookEdit`, `bash: false` denies `Bash`, `spawn: []`
  denies `Agent`/`Task` — matching OpenCode's deny-by-exception model.
- **`steps` now reaches Claude as `maxTurns`** (it was dropped as
  "no concept"; Claude has one).
- **`dialogs: false` is now enforced in OpenCode** (`question: deny`);
  omitting the line left the question tool available in defiance of the
  declared intent. All eight specialist subagents in both frameworks are
  now `dialogs: false` — matching the protocol's "a subagent never prompts
  the user; it returns a `Questions:` section" rule, which is also the only
  behavior Claude can express (AskUserQuestion is never available to Claude
  subagents). The onto orchestrator (primary) keeps its dialogs.
- **The unrecognized `mode:` line is gone from Claude variants** (Claude
  has no such frontmatter field).

### New in v0.6.0 — four model tiers, project-scoped model settings & MCPs, closed tier names

**`review` is the fourth model tier.** Model routes are now `architectural`
(orchestrate/design), `coding` (implement), `review` (judge others' work),
and `trivial` (cheap lookups) — and a model-backed config must declare all
four per enabled tool (**breaking**: existing three-route configs fail at
load until a `[models.<tool>.review]` block is added). The onto and to
reviewers and skeptics now run on the review tier instead of borrowing the
architectural one, in both Claude Code and OpenCode; the catalog is bumped
to 0.5.0 and re-materializes on the next apply.

**Route-derived default-model keys follow scope.** When every model-backed
resource (framework, command, subagent) enabled for a tool is
project-scoped, the `[models.<tool>.*]`-derived default-model keys now
project into the project-level config the tool merges over its global one
(`<repo>/opencode.jsonc` `model`/`small_model`;
`<repo>/.claude/settings.json` `model`) instead of the global file — one
repository's workflow models no longer become every other session's
defaults, and two repositories no longer fight over the same global keys.
Previously-applied global keys are pruned automatically on the next
`apply`. Any user-scope model-backed resource, and all explicit
`[settings.<tool>]` keys, keep today's global projection.

**MCP servers take a `scope`.** `[mcps.<name>] scope = "project"` projects
the server into the project-level config (Claude Code `<repo>/.mcp.json`;
OpenCode `<repo>/opencode.jsonc`) instead of the global one, so a
repository's servers no longer run in every other session. Default stays
`user` (global, today's behavior); codex remains user-scope only and a
project-scoped codex target fails at load. A previously-global server whose
scope changes migrates automatically on the next `apply`.

**Tier and role names are enforced.** `[models.<tool>.<level>]` with a
level outside `architectural`/`coding`/`trivial` now fails at load naming
the offender, and an agent frontmatter `role:` outside the same three tiers
fails at render — both were silent no-ops before (an unknown role rendered
the agent with no model at all).

### New in v0.5.1 — documentation rewrite

Docs only; the binaries are identical to v0.5.0. The README and every living
guide were rewritten for accuracy and directness: the source matrix is now
stated correctly everywhere (frameworks accept `builtin:`, `local:`, and
digest-pinned `remote:`; onto and `to` are mutually exclusive), stale
"`to` is planned" claims are gone, and the reference guides were re-checked
against the shipped binaries' command surfaces.

### New in v0.5.0 — live task lists, hardened `to`, principle guides

**The task list is live state — in both frameworks.** Discovered work is
appended to the checklist (with its files and verification) *before* its code
is written; checkoffs ride each task's own commit; tasks are never renumbered
or deleted (`SUPERSEDED` instead), so a fresh session always resumes from the
first unchecked task. onto gets this in onto-build, its templates, the
presets, and the subagent protocol (implementers report discovered work, the
coordinator appends it); `to` gets the same discipline adapted to its plan
contract.

**`to` grew teeth without growing ceremony:**

- **Plan contract**: every task carries `Files:` / `Change:` / `Verify:`
  fields plus a whole-change `Final Verify:` line; notes and verification
  evidence live in the same archived `plan.md`. `to doctor` diagnoses
  contract violations (line-numbered), wedged archives, and version skew;
  `--quiet` is the enforcement hook primitive.
- **Crash convergence**: an interrupted `done`/`abandon` no longer wedges a
  change — re-running the same command completes the archive.
- **Date-prefixed archives** (`docs/tasks/archive/<date>-<name>/`) free
  change names for reuse; mutating commands take a fail-fast workspace lock.
- **`to done --evidence "<text>"`** records what was asserted, verbatim and
  unchecked, so a real verification is distinguishable in the archive.
- **`to handoff`** now excerpts what a resuming session needs: the plan head,
  every unchecked task contract, `Final Verify:`, and bounded notes.

**Docs**: the `to` guides split into [workflow concepts](https://github.com/noviopenworks/homonto/blob/main/docs/guides/to-workflow.md)
and a [command reference](https://github.com/noviopenworks/homonto/blob/main/docs/guides/to-reference.md)
(mirroring onto's pair), and two principle guides —
[YAGNI](https://github.com/noviopenworks/homonto/blob/main/docs/guides/yagni.md) and
[KISS](https://github.com/noviopenworks/homonto/blob/main/docs/guides/kiss.md) —
map where each framework structurally enforces building only what's needed,
simply. Framework versions: onto 0.3.2, to 0.2.0; catalog 0.4.0.

### New in v0.4.0 — the `to` framework

`to` is the minimal coding framework for LLMs: **plan → do → done**, a
bookkeeper binary (`init`, `new`, `status`, `phase`, `done --verified`,
`abandon`, `handoff`; structured `--json` output on each of those workflow
commands), and the `builtin:to` catalog
framework — a `/to` dispatcher, three phase skills, a vendored `to-no-slop`,
and four **sequential-only** specialist subagents adapted from onto. Changes
live under `docs/tasks/` and archive on done. Design and rationale:
`docs/to-framework-design.md`.

Two deliberate properties to know before adopting it:

- **onto and `to` are mutually exclusive.** Declaring both frameworks in one
  `homonto.toml` fails at load — pick one workflow per repository (onto for
  evidence-gated enterprise changes, `to` for simple development). There is no
  escalation path between their state formats.
- **`to done --verified` is self-asserted.** The binary records the checkbox;
  it observes no evidence. The verification rigor lives in the `to-done`
  skill (real verify run + a single adversarial skeptic pass), not in a gate.

### Breaking in v0.3.0 — comet, openspec, and superpowers removed

The catalog now ships **only homonto-native frameworks**: `onto` (and, since
v0.4.0, `to`) — plus the loose framework-agnostic
skills/commands (`handoff`, `grilling`), which are a separate channel and
unaffected. A config declaring `[frameworks.comet]`, `[frameworks.openspec]`,
`[frameworks.superpowers]`, or `builtin:comet-navigator` now fails at load
with `catalog: unknown framework` / `unknown subagent`; remove the
declaration (their projected links are pruned on the next apply) or vendor
the content yourself via a `local:` framework / pinned `remote:` source.
v0.2.2 is the last release carrying them. Rationale: ADR 0015.

### New in v0.2.2 — dirty-workspace support

The close gate no longer treats every uncommitted path the same. `onto dirt
[change] [--json]` classifies each dirty path — `own` (the change's own
`docs/changes/<name>/` evidence), `change` (another change's docs), `source`
(everything else) — and `onto advance`/`onto close` now tolerate `change`
dirt: one change's in-flight artifacts no longer deadlock another change's
close. What does block (`own` + `source`) is listed right in the refusal
instead of a bare "dirty worktree blocks close". The onto skills gained a
dirty-workspace protocol (attribution stays with the agent; the binary owns
classification).

### Fixed in v0.2.1 — deep-review findings

**onto's terminal states are now actually terminal.** An abandoned change could
archive as a success, have its evidence tokens forged via `onto set`, and merge
its never-accepted deltas into the living specs; all three paths now refuse.
`merge-deltas` recovers from a crash between its per-file writes instead of
wedging the change forever; `onto scale` errors without a recorded base ref
instead of silently measuring an empty diff as "light"; dependency resolution
is an exact name match (dep `auth` is no longer satisfied by an archive named
`…-refactor-auth`); a close crash can no longer leave `archived: true` at the
original path; `doctor` skips abandoned changes and `--quiet` is now fully
quiet.

**homonto re-materializes when framework CONTENT changes.** Editing a `local:`
framework's resources — or repinning a `remote:` framework's digest, which is
how a patched resource ships — used to be ignored forever ("No changes"). The
materialize gate now digests source content. Related: `plan` surfaces a pending
re-materialization (text + `--exit-code` 2) instead of disagreeing with apply;
renamed/de-declared resources are GC'd from `.homonto/catalog/` instead of
lingering where the adapters' variant-preference could resurrect them; and a
per-subagent model override is validated no matter what the entry's `targets`
say (an unvalidated value could previously reach a live agent file), with
conflicting overrides for one builtin now a deterministic load error.

### Breaking in v0.2.0 — `effort` and `variant` now do something

They were **required by validation and projected nowhere**: homonto forced you
to write two fields it then discarded — and never checked, so real configs
filled up with values no tool accepts (`effort = "normal"`, `variant = "max"`,
even `effort = "n"`). Now they are **optional, validated, and actually
projected** into each tool's own dialect:

| | Claude Code | OpenCode |
|---|---|---|
| `variant` | rendered *into* the model string (`opus[1m]`); **alias-only**, `1m` is the only documented one | a first-class `variant:` field, any provider-defined value |
| `effort` | a real field: `low`, `medium`, `high`, `xhigh`, `max` | **no such concept** — declaring it is now an error |

**You may need to edit your config.** A route naming just a `model` is now
complete, so the simplest fix is to delete values you were only writing to
satisfy the old rule. Otherwise the loader tells you exactly what is wrong:

```
parse config: models.claude.coding effort "normal" is not a Claude effort level (low, medium, high, xhigh, max)
parse config: models.opencode.coding sets effort "high", but OpenCode has no effort setting — use variant, or drop it
```

**New:** retune one agent without restating its tier — each field wins field by
field, and no `source` is needed for an agent a framework installed:

```toml
[subagents.onto-skeptic.claude]
effort = "max"
```

### Breaking in v0.2.0 — onto's subagents are namespaced `onto-*`

Every resource the onto framework ships is now namespaced, so installing onto
cannot collide with another framework's — or your own — agent of the same
generic name. Two builtin subagents were renamed:

| Old | New |
|---|---|
| `builtin:code-reviewer` | `builtin:onto-reviewer` |
| `builtin:codebase-explorer` | `builtin:onto-explorer` |

If you declare either **standalone** in a `[subagents.*]` table, update its
`source` — an old name now fails at load with `catalog: unknown subagent`. If
you install them via `[frameworks.onto]`, apply handles the rename for you: the
old agent files are pruned and the new ones projected. (The onto skills, its
commands, and the `onto` dispatcher itself are unchanged; `onto` is the
namespace root.)

### Fixed in v0.2.0 — subagents now track their model routes

Changing a `[models.<tool>.<role>]` route did **not** re-render the subagents
stamped from it. The projected agents stayed frozen at the model they were first
materialized with, while the tool's own `setting.model` — re-read from the routes
on every apply — moved correctly: one config, two different answers. If you have
edited a model route since installing a framework or subagent, **upgrade and run
`homonto apply`** to re-stamp your agents; verify with
`grep '^model:' .homonto/catalog/subagents/*.md`.

Three related defects went with it: a deleted rendered agent variant is now
restored instead of stranding a dangling symlink that `plan`/`status`/`doctor`
all called healthy; `apply` now re-materializes the catalog even when the
projection plan is empty; and `doctor` no longer reports a permanent, unfixable
finding for an OpenCode-primary agent's by-design absent Claude variant.

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
