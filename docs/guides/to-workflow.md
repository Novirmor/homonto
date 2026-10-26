# The to workflow

**to** is the minimal coding framework for LLMs that homonto ships as a
bundled framework. It has two halves that work together:

- the **`to` binary** (built from `cmd/to/`, installed beside `homonto`) — the
  bookkeeper: it creates change workspaces, records the one phase transition,
  archives finished changes, and answers `status`/`handoff`/`doctor` from
  state only it writes; and
- the **`to-*` skills** (materialized from the builtin catalog by
  `homonto apply`) — the agent-facing process prose that drives the work
  inside each phase.

The binary owns the *state*; the skills own the *discipline*. A change moves
through three phases in a fixed order:

```
plan → do → done
```

`done` and `abandoned` are terminal (the change is then archived). Each change
tracks its phase in a `to-state.yaml` inside its workspace directory,
always written through the binary and never by hand.

Unlike onto, **to enforces no evidence gates**: `to done --verified` records a
self-asserted checkbox, not observed proof. The verification rigor lives
entirely in the `to-done` skill (a real verify run plus at least one
adversarial skeptic pass). That trade is the product: much less ceremony per
change. The binary still checks installation, phase/state, and applicable
source cleanliness; it does not certify verification. Design rationale:
[to-framework-design.md](../to-framework-design.md).

The coordinator triages review findings against supported scenarios and agreed
scope, retains finding IDs/dispositions, and bounds repair rechecks to affected
claims and adjacent risks. Proven data loss, credential exposure and failed core
acceptance remain blocking. Review reports and unfinished checks are remaining
work, not completion.

### Recovery after archive

`to status --json` lists only active tasks. The dispatcher also inspects matching
archives before creating another task, using recorded identity, candidate/tree
OIDs and targets (plus canonical PR identity for PR work). An archived done task
may still need integration or publication. Resume the first missing authorized
action through `to-done` without rerunning archival or modifying the archive;
retain post-archive evidence in the handoff. Abandoned archives are not verified
delivery inputs. A requested review-ready PR additionally needs observed
OPEN/non-draft state and required checks passing on the delivered head; local
verification and `--verified` do not prove that. See
[ADR 0071](../adr/0071-align-entrypoints-and-delivery-recovery.md).

## onto and to — complementary by selection

Pick the workflow per change, not per repository. Declaring both
`[engines.onto]` and `[engines.to]` in one `homonto.toml` is valid
(ADR 0042): their records live in disjoint directories (`changes/` vs
`tasks/`) and their agents and commands are namespaced, so both project side
by side and the request decides which dispatcher the shared `homonto`
coordinator loads (ADR 0045). Pick **onto**
for changes that need spec deltas, dependency graphs, and artifact/evidence
checks on normal transitions; pick **to** for simple development where that
machinery costs more than it protects. `no` may also be installed. Each task
selects exactly one engine and retains it on resume (ADR 0068). The existing
promote/demote CLI bridges remain available for explicit legacy conversion;
they are not part of the workspace-task flow and cannot preserve its recorded
engine/generation identity. Task instructions never switch engines automatically.

## Install and enable

```bash
go install github.com/noviopenworks/homonto/cmd/to@latest
to version
```

Or install `to` together with `homonto` through the [interactive
installer](getting-started.md#1-install): it asks which binaries you want,
verifies the release archives against `SHA256SUMS`, and prints PATH
instructions without editing your shell configuration. On confirmation, it can
also scaffold the current directory with `homonto init`.

The mutating commands require the framework to be **declared and applied
through homonto first** — this is how the skills land in your tools:

```toml
[engines.to]
source = "builtin:to"
scope = "project"
# plus a [subagents.<name>.<tool>] model block per to agent — see the
# configuration reference
```

Then `homonto apply`. It also installs the slash commands: `/to` (the
dispatcher — it finds the active change via `to status --json` and routes),
the command-only explicit-user `/to-bypass`, plus `/to-plan`, `/to-do`, `/to-done`,
and `/to-no-slop`.

The install also adds the shared `homonto` knowledge skill and the shared
`homonto` coordinator agent (ADR 0045 — one primary serves both workflows).
It owns each workflow mutation, decision, commit, and delegation. Every
`to-*` workflow command routes to it.

## The layout

Each change is a directory `<workflow-root>/tasks/<name>/` holding `to-state.yaml`
(written **only** by the binary) and `plan.md` (written by the agent during
plan). Finished changes move to `<workflow-root>/tasks/archive/<date>-<name>/`; the date
prefix frees the name for reuse. By default `to` is git-blind. For one change
spanning declared repositories, create it in the config repo with repeatable
`--repo` aliases, for example `to new release --repo api --repo web`. The
state and plan still live only in the config repo; before `to done`, the
config repo and each selected repo must be clean and Git-readable.

## The plan contract

`plan.md` is the change's single durable human-authored record. It starts
with the goal, approach, and scope boundary, followed by ordered tasks:

```markdown
- [ ] <Concrete outcome>
  - Files: `<paths and, when useful, symbols>`
  - Change: <behavior or contract to add, remove, or preserve>
  - Verify: `<exact command>` — <specific passing signal>
```

Implementation and its focused tests stay in the same task. **The task list
is live during `do`**: discovered work is appended with the same contract
(outcome suffixed `(discovered <date>)`, placed before `Final Verify:`)
before its code is written; tasks are checked off in the commit that
completes them; a task made unnecessary is checked as
`- [x] SUPERSEDED: <reason>` rather than deleted. Decisions and declined
review findings go under `## Notes`. A distinct `Final Verify:` line names
the whole-change command; its literal result, coverage gaps, and the skeptic
verdict go under `## Verification`. One archived artifact carries planning,
recovery, review, and final evidence.

## Phase walkthrough

- **plan** (`/to-plan`) — ground the approach in reading (dispatch
  `to-explorer` for multi-file questions), write `plan.md` per the contract,
  de-slop it, then `to phase <name>`.
- **do** (`/to-do`) — execute independent repositories in parallel, with exactly
  one repository per `to-implementer` assignment and one writer per checkout.
  The orchestrator verifies against the repository, one or
  more `to-reviewer` passes judge the diff, findings are fixed or declined in
  writing, the task is checked off in its own commit.
- **done** (`/to-done`) — run `Final Verify:`, obtain at least one completed
  `to-skeptic` pass on the final candidate, record the outcome under
  `## Verification`, then `to done <name> --verified --evidence "…"` archives
  the change.

Starting or resuming `to` continues across these phase boundaries in the same
invocation through the full success endpoint, including required verification,
archival, integration, and authorized publication. A finished plan checklist
still routes to `to-done`, not a completion report. The orchestrator stops earlier
when the user names an endpoint or asks to pause, a genuine question blocks
progress, or a hard blocker remains after permitted recovery is exhausted or
unavailable. It reports hard blockers with evidence and the next action, without
inventing a question or claiming completion. It chooses reversible technical
defaults and asks only when product intent, scope, or authorization is missing.

`to abandon <name>` is the terminal exit without done, from any phase.

## Specialist subagents

onto's cast, adapted. Delegate heavily — concurrency is decided by what an
agent writes, not by which agent it is:

| Subagent | Role | Concurrency |
|---|---|---|
| `to-explorer` | Read-only, no-shell codebase questions; returns conclusions, not dumps. | Concurrent — one per question |
| `to-implementer` | Executes one repository assignment; returns edits and evidence, leaving commits to the parent. | Parallel across repositories; serial per checkout |
| `to-reviewer` | No-shell review of each supplied diff for correctness, security, contract, and clarity. | Concurrent — one per lens |
| `to-skeptic` | No-shell fresh-context passes in `to-done`, prompted to refute the "it works" claim and request exact probes. | Concurrent — one per lens |

Read-only agents may run concurrently, but reviewers need stable candidates:
pause writers in reviewed checkouts. Workers may read all declared repositories.
Per-repo reviews precede combined interface review and configured integration
checks. The user chooses isolation once per task, with per-repo exceptions;
shared workspace tasks preserve that choice and candidate-bound results.
Bookkeeping — `plan.md` edits, checkoffs, commits — stays with the
orchestrator and never runs concurrently.

## Surviving context loss

`to handoff <name>` prints a compact recovery pack — identity, phase, the
safe next skill, and a plan excerpt built for resuming: the head, every
unchecked task contract, `Final Verify:`, and bounded notes/verification
sections. A fresh session reads it, then continues from the first unchecked
task. `to doctor` is the read-only health check; `--quiet` reports diagnostics
through its exit code for scripts or custom hooks. It does not block session
completion — see [enforcement](enforcement.md).

## Tooling providers

Like onto, `to` names no tool of its own. Declare what you use in `[tooling]`
and `homonto apply` renders it into the dispatcher's generated
`references/tooling.md`:

```toml
[tooling]
shell_proxy = "rtk"       # or "none" (default)
code_intel  = "graphify"  # or "okf", or "none" (default)
```

Both default to `none`, so declaring nothing means the preflight names
nothing. A declared-but-missing provider warns and the workflow proceeds; it
never halts. homonto never installs or runs a provider. Full reference:
[configuration](configuration.md#tooling).

## Workspace tmp

A `[tmp]` table declares one gitignored scratch directory (default `.tmp`)
that every writable agent uses without prompts: `homonto apply` creates it
and renders `references/tmp.md` into the dispatcher and the shared `homonto`
skill naming the path. Transient files a later step must find again —
handoff drafts, fetched packets — belong there, not in anonymous `mktemp`
paths. homonto never deletes its content. Full reference:
[configuration](configuration.md#workspace-tmp--tmp).

## Where the details live

Every command, flag, and crash-safety behavior:
[to reference](to-reference.md). The principles the skills enforce:
[YAGNI](yagni.md) and [KISS](kiss.md).
