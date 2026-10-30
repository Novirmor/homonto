# The catalog — every package, skill, command, and agent

The catalog is the content compiled into the `homonto` binary (catalog version
0.34.0). Declaring a package installs its whole bundle; standalone items can be
declared individually with a `builtin:` source. This page states what each
item is for. How the pieces work together is in [workflows](workflows.md).

## Packages at a glance

| Package | Table | Skills | Commands | Agents | Binary |
|---|---|---|---|---|---|
| `onto` | `[engines.onto]` | `homonto`, `onto`, `onto-open`, `onto-design`, `onto-build`, `onto-verify`, `onto-close`, `onto-light`, `onto-no-slop` | `/onto`, `/onto-open`, `/onto-design`, `/onto-build`, `/onto-verify`, `/onto-close`, `/onto-light`, `/onto-no-slop`, `/onto-bypass` | `homonto`, `onto-explorer`, `onto-implementer`, `onto-reviewer`, `onto-skeptic` | `onto` |
| `to` | `[engines.to]` | `homonto`, `to`, `to-plan`, `to-do`, `to-done`, `to-no-slop` | `/to`, `/to-plan`, `/to-do`, `/to-done`, `/to-no-slop`, `/to-bypass` | `homonto`, `to-explorer`, `to-implementer`, `to-reviewer`, `to-skeptic` | `to` |
| `no` | `[engines.no]` | `no` | `/no` | `no-implementer`, `no-reviewer` | — |
| `h` | `[workflows.h]` | `homonto`, `h-spike-issue`, `h-resolve-issue`, `h-review-pr`, `h-continue-pr`, `h-resolve-conflicts`, `h-review-batch` | `/h-spike-issue`, `/h-resolve-issue`, `/h-review-pr`, `/h-continue-pr`, `/h-resolve-conflicts`, `/h-review-batch` | `homonto`, `h-spike`, `h-review` | — |

`onto`, `to`, and `h` also install the `homonto-workflow` OpenCode plugin
([OpenCode integration](opencode.md)). Items shared between packages
(`homonto` skill and agent) are installed once.

Standalone items: skills `handoff` and `grilling`; commands `/handoff` and
`/grill`; plugin `permission-observer`.

## Skills

### Shared

| Skill | Purpose |
|---|---|
| `homonto` | Reference knowledge, not a dispatcher: how homonto config and projection work, how to choose between engines, and the shared policies every workflow follows. Its `references/` hold those policies: `workflow-selection.md` (which engine for new work, preset eligibility), `autonomy.md` (decide before asking, trusted shell, root bootstrap), `execution.md` (dispatch capability, parallel repositories, focused exploration, candidate-bound verification), `review-triage.md` (proportionality, finding dispositions, bounded rechecks), `workspace-policy.md` (dirty work: preserve / isolate / cleanup), `publication.md` (verified PR delivery and GitHub draft tools), `debugging.md`. Apply also generates `references/workspace.md` (exact roots) and `references/tmp.md` here. |

### onto engine

| Skill | Purpose |
|---|---|
| `onto` | **Dispatcher.** Preflights the `onto` binary and declared tooling, discovers or creates the change, derives the real phase from files (not just the state file), and routes to the phase or preset skill. Owns the delegation rules. References: `delivery.md` (drive to the requested endpoint), `recovery.md`, `discovery.md`, `state-yaml.md`, `dirty-workspace.md`. |
| `onto-open` | **Phase 1.** Clarifies the requirement, checks whether the work should be split into several changes, runs `onto new`, writes `proposal.md` (why, scope, out of scope), and records `proposal-approved`. |
| `onto-design` | **Phase 2.** Explores the code for ground truth, evaluates 2–3 approaches, selects one from evidence and your constraints, then writes `design.md`, ADR drafts, delta specs with testable scenarios, and `tasks.md`; records `approach-confirmed` and the isolation choice. No implementation code. |
| `onto-build` | **Phase 3.** Writes `plan.md` (one `## Task N.M` per `tasks.md` item: files, steps, verification), then executes task by task — implementer, review, one commit per task, checkoff in the same commit. Root-cause-first debugging on failure; discovered work is appended before it is coded. References: subagent, worktree, TDD, and receiving-review protocols. |
| `onto-verify` | **Phase 4.** Measures the diff (`onto scale`), applies risk overrides (security-sensitive → full), checks every delta-spec scenario with fresh command output, runs the regression suite, dispatches at least two skeptics (conformance + robustness), triages findings into repairs or justified dismissals, writes `verification.md`, records evidence and `verify-result`. |
| `onto-close` | **Phase 5.** Validates the close plan, merges delta specs (`onto merge-deltas`), numbers and accepts ADRs, resolves the guides obligation, archives (`onto close`), integrates (merge or PR), and records the receipt (`onto complete-integration`). |
| `onto-light` | **Preset: light.** A bounded bug fix (failing reproduction test first) or a small non-bug change: open-lite (proposal + tasks), build, verify without skeptics, close. Escalates to full onto when scope or risk outgrows it. |
| `onto-no-slop` | Edits onto prose (proposals, designs, ADRs, guides, reports, commit messages) for clarity while keeping technical meaning, evidence, and machine-read markers intact. Adapted from stop-slop (MIT). |

### to engine

| Skill | Purpose |
|---|---|
| `to` | **Dispatcher.** Preflights the `to` binary and tooling, finds active changes (`to status --json`) and interrupted archived deliveries, and routes `plan → do → done`. Owns delegation rules. Reference: `recovery.md` (resume integration/publication after archive). |
| `to-plan` | **Phase 1.** Grounds the approach, writes `plan.md` — goal, boundary, a short list of task contracts (`Files:`, `Change:`, `Verify:`), and a `Final Verify:` line — de-slops it, then `to phase`. Reference: `task-examples.md`. |
| `to-do` | **Phase 2.** Runs each task through implementer → coordinator verification → reviewer passes → fix or decline findings in writing → check off in its own commit. Parallel across repositories, one writer per checkout. |
| `to-done` | **Phase 3.** Runs `Final Verify:`, obtains at least one completed `to-skeptic` pass on the unchanged final candidate (or records the permitted direct-review fallback), records results under `## Verification`, runs `to done --verified`, and completes authorized integration and publication. |
| `to-no-slop` | Edits to prose (plans, notes, verification records, commit messages) for clarity without touching task checkboxes, commands, or their output. |

### no engine

| Skill | Purpose |
|---|---|
| `no` | Inventory of the two `no` workers. Its `references/execution.md` is the coordination contract for `/no` and for `h-resolve-issue` when `no` is chosen: one implementer per repository, parent-owned commits, review before delivery, optional shared workspace-task bookkeeping. |

### h workflow

| Skill | Purpose |
|---|---|
| `h-spike-issue` | Research one GitHub issue without changing code: fetch it, dispatch `h-spike` workers, validate their citations, and produce an implementation brief (findings, candidate code paths, unknowns, risks, approach, workflow fit). Optionally posts the brief as an issue comment after you approve the shown draft. |
| `h-resolve-issue` | Take an issue to a verified PR: spike first, select an engine (`no`, `to`, `onto light`, full `onto`), drive it to completion, push, and open one PR that closes the issue. |
| `h-review-pr` | Review one PR with full context (metadata, every review thread, diff, linked issues, checks), delegate analysis to `h-review`, validate findings, show a draft. Posts only after explicit approval of that draft. References: `context-pack.md`, `draft-format.md`. |
| `h-continue-pr` | Address feedback on an existing PR: collect requested changes and unresolved threads, check out the exact head, make the smallest verified fixes, push to the same branch, post one summary comment, resolve only threads actually addressed. Needs no engine. |
| `h-resolve-conflicts` | Resolve only an existing PR's merge conflicts: pin base/head, preserve both sides' intent, verify and push normally to the same head. No engine, unrelated hardening or implicit comments. A conflict-free pair is a no-op; broader repairs need native-dialog scope approval. |
| `h-review-batch` | Review many open PRs (default 10, filter by author) or a list: one `h-review` worker per PR, all drafts shown together, one publication decision. |

### Standalone

| Skill | Purpose |
|---|---|
| `handoff` | Compress the current conversation into a handoff document (goal, state, done, next, key files, decisions, gotchas) so a fresh agent can continue. Writes to the `[tmp]` directory when declared. Adapted from mattpocock/skills (MIT). |
| `grilling` | Interview you, one question at a time, to stress-test a plan or design before building — upstream decisions first, a recommended answer for each, a numbered summary at the end. Adapted from mattpocock/skills (MIT). |

## Commands

Every workflow command runs in the `homonto` coordinator (`agent: homonto`)
except `/no`, `/grill`, and `/handoff`, which run in your current agent.
Arguments after the command describe the work.

| Command | What it does |
|---|---|
| `/onto [work]` | Start or resume onto. New work without a chosen path gets one question: light or full. |
| `/onto-open [work]` | Select **full** onto for new work, even if a preset would fit. |
| `/onto-design`, `/onto-build`, `/onto-verify`, `/onto-close` | Run or resume that phase, then continue through the rest. |
| `/onto-light [work]` | Run the light preset end to end. |
| `/onto-no-slop [artifact]` | De-slop an onto prose artifact. |
| `/onto-bypass` | User-only emergency: `onto bypass <change> --to <phase\|archive> --reason "…"`. Never suggested by other skills. |
| `/to [work]` | Start or resume to. |
| `/to-plan`, `/to-do`, `/to-done` | Run that phase, then continue. |
| `/to-no-slop [artifact]` | De-slop a to prose artifact. |
| `/to-bypass` | User-only emergency: `to bypass <change> --to <plan\|do\|done\|archive> --reason "…"` (records `verified: false`). |
| `/no [work]` | Run work through the `no` implementer and reviewer. |
| `/h-spike-issue <issue>` | Research an issue into a brief. |
| `/h-resolve-issue <issue>` | Resolve an issue to a verified PR. |
| `/h-review-pr <pr>` | Draft a review of one PR. |
| `/h-continue-pr <pr>` | Address a PR's feedback and update it. |
| `/h-resolve-conflicts <pr>` | Resolve only merge conflicts and update the same PR head. |
| `/h-review-batch <repo-url \| prs…> [limit N] [author USER]` | Draft reviews for many PRs. |
| `/handoff [focus]` | Write a handoff document. |
| `/grill [plan]` | Stress-test a plan by interview. |

Issue references: a GitHub issue URL or `OWNER/REPO#NUMBER`. PR references: a
URL, `OWNER/REPO#NUMBER`, a number, or a unique head branch.

Every command continues to its success endpoint in the same invocation unless
you name an earlier endpoint ("just plan it", "stop after verify") or ask to
pause.

## Subagents

| Agent | Package | Role |
|---|---|---|
| `homonto` | onto, to, h | **The coordinator** (OpenCode primary agent). Selects the engine, loads the dispatcher, owns state changes, commits, integration, and publication, delegates to specialists, triages their findings, and drives work to the requested endpoint. Only agent that asks you questions. |
| `onto-explorer` | onto | Answers "how does X work / where does Y live" by reading across many files and returning cited conclusions, not dumps. One per independent question, in parallel. |
| `onto-implementer` | onto | Executes one bite-sized task from a precise spec, runs its verification, returns a diff summary. Does not plan, judge scope, or delegate; reports discovered work instead of doing it. |
| `onto-reviewer` | onto | Reviews a diff for correctness, security, contract, and clarity; findings ranked by severity with the smallest fix. Per task in build and across the diff in verify; one per lens. |
| `onto-skeptic` | onto | Attacks a verification claim from a fresh context, prompted to **refute, never approve**. At least two per verify (conformance and robustness lenses). Requests exact probes for the coordinator to run. |
| `to-explorer` | to | Same as `onto-explorer`, for to. |
| `to-implementer` | to | Executes one assignment in one repository and returns edits plus evidence; the coordinator commits. Parallel across repositories, one per checkout. |
| `to-reviewer` | to | Reviews the implementer's diff; one per lens. |
| `to-skeptic` | to | Attacks the "it works" claim before archiving. Each completed pass must describe the unchanged final candidate; a code change means a new pass. |
| `no-implementer` | no | Implements the assigned change and runs the verification the task warrants. No delegation, workflow state, or publication. |
| `no-reviewer` | no | Reviews one implementation diff for correctness and task conformance. |
| `h-spike` | h | Researches an issue from a supplied packet; returns findings, candidate code paths, unknowns, risks, and a workflow recommendation. |
| `h-review` | h | Reviews a PR from a supplied context packet (metadata, diff, linked issues, checks, existing threads); ranked findings. Several run concurrently across PRs or lenses. |

Permissions for each are in [subagents](subagents.md#what-the-shipped-agents-may-do).
Model blocks are required for every one you install ([configuration](configuration.md#agent-models--subagentsnameopencode)).

## Plugins

| Plugin | Installed by | Purpose |
|---|---|---|
| `homonto-workflow` | onto, to, h (default) | OpenCode 2 workflow context for model requests and compaction, the **Open workflow status** panel, `homonto_status` / `homonto_handoff` tools, idle notifications, and — with `h` — `homonto_github_draft` / `_status` / `_publish` draft tools. Read-only toward workflow state. |
| `permission-observer` | `[plugins.opencode.permission-observer]` | Watches explicit shell approvals in memory and suggests an exact `bash_allow_add` entry after two approvals of the same command. |

## Inspect what is installed

```console
$ homonto explain                       # every managed resource
$ homonto explain subagent onto-skeptic # one resource: origin, destination, last change
$ ls .homonto/catalog/skills            # materialized catalog content
$ cat .homonto/catalog/subagents/onto-skeptic.opencode.md   # rendered agent
```
