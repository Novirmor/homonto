# onto reference — commands, flow, and gates

The precise reference for the `onto` binary: how a change **enters** the
workflow, how it **moves** between phases, the exact **gates** each
transition enforces, and every command. For the conceptual overview and the
skills side, read [the onto workflow](onto-workflow.md) first.

The **`onto` binary owns the state and the gates**; the `onto-*` skills own
the work inside each phase. Every state change goes through the binary
(`onto new`, `onto set …`, `onto advance`, `onto close`). The skills never
hand-edit `onto-state.yaml`, and the phase is always cross-checked against
real file state.

Most commands take `--dir <root>` (default `.`) to select the workspace
root. Mutating commands require the onto framework to be installed by
homonto. Read-only commands do not write; `onto dirt <change>` reads
`homonto.toml` only when that change records selected declared repos.

## General flow

```
                 ┌─────────────────── onto advance (one phase per call) ───────────────────┐
                 ▼                                                                          │
   onto new → [ open ] → [ design ] → [ build ] → [ verify ] → [ close ] ── onto close ──→ archived/pending
                                                                                                  │
                                                   done ← onto complete-integration ←──────────────┘

   presets:   --workflow fix / tweak run a reduced path (open-lite → build → verify → close),
              and upgrade to the full path when scope grows.

   failure:   onto abandon <change>  →  abandoned  (the unsuccessful terminal state)
```

A change tracks its phase and evidence in
`docs/changes/<name>/onto-state.yaml`. The phase set is exactly
`open → design → build → verify → close`; `close` is the terminal phase
(reached by advancing), after which `onto close` archives the change and records
pending Git integration. `done` is derived only after `onto
complete-integration` records the merge commit or opened PR URL.
There is no `archive` phase.

## Entering — `onto init` and `onto new`

**`onto init [--dir <root>]`** scaffolds the `docs/{changes,specs,adr,guides}/`
layout, idempotently. It reports created vs. skipped paths and never
overwrites existing content.

**`onto new <name> [--workflow full|fix|tweak] [--repo <declared-name>]`** creates
`docs/changes/<name>/` with:

- `onto-state.yaml` at **phase `open`**, `workflow: full` (the default);
- a `proposal.md` skeleton — plus `tasks.md` **only for the fix/tweak
  presets**; a full change's `tasks.md` is derived later, in design.

It requires the framework installed, refuses to clobber an existing change,
and validates that the name is kebab-case with no path traversal.

`--repo` is repeatable and records selected names from `[repos]` in
`onto-state.yaml`; the config repo is implicit. It never creates workflow
files in sibling repos. Omit it for the existing single-repo behavior.

## Advancing — `onto advance <change> [--to build]`

Each call attempts **one** transition and writes nothing unless every gate
below passes, in this order. `--to build` is **preset-only** and the only
accepted value: it walks the gated advances up to build in one call, so a
`fix`/`tweak` change reaches build without two ceremonial advances. Every gate
below still applies to each step it walks.

1. **Framework installed** (the install gate) and a **valid change name**.
2. State **loads** and the change is **not abandoned**.
3. The current phase has a **next phase** (advancing from `close` is an
   error).
4. **Required artifacts** for the *current* phase all exist — **workflow-aware**
   (they accumulate). A full change derives its task list *from* the
   confirmed design, so `tasks.md` gates the **design** exit, not the open
   exit. The fix/tweak presets skip design and decompose at open-lite, so
   their `tasks.md` gates the **open** exit and no `design.md`/`plan.md` is
   ever demanded (this is what lets a preset advance straight through
   design and build):

   | Leaving phase | full | fix / tweak |
   |---|---|---|
   | `open`   | `proposal.md` | `proposal.md`, `tasks.md` |
   | `design` | + `design.md`, `tasks.md` | *(pass-through — no `design.md`)* |
   | `build`  | + `plan.md` (and all tasks checked) | all tasks checked (no `plan.md`) |
   | `verify` | + `verification.md` | + `verification.md` |

   An empty or unknown workflow is treated as full (strictest).

5. **Leaving `build`:** `tasks.md` has **no unchecked items** (`- [ ]`).
6. **Evidence / entry tokens** (recorded via `onto set`, not inferred from
   files):
   - **Leaving `open`** (full only): `proposal_approved` is non-empty — the
     open phase's proposal-review summary.
   - **Entering `build`** (design→build): `isolation` is set (`branch` or
     `worktree`), so planning and build work is never committed unisolated;
      **and** `approach_confirmed` is non-empty (full only) — the design
      phase's selected approach and basis; **and** every dependency is complete;
      **and** the change is **not in a dependency cycle** (no valid build order
      exists).
   - **Leaving `verify`** (verify→close): `verify.result == pass`.

   These review tokens preserve the decision across sessions: the binary refuses
   the transition while a token is empty. The coordinator records a truthful
   review summary; Git records who wrote it. Presets are exempt from the two
   full-only tokens but still record close-plan validation.
7. **Worktree cleanliness:** entering `close` is **blocked** by uncommitted
   paths in the config repo and every repo selected with `onto new --repo`.
   The config repo keeps the carve-out for another active change's
   `docs/changes/<other>/`; every external-repo path blocks. A missing alias,
   unavailable Git worktree, or undeterminable result also blocks. The refusal
   labels the repository; `onto dirt <change>` shows the full classified list.
   Every other transition only **warns** on config-repo dirt and proceeds.

A failed gate exits non-zero and leaves the recorded phase unchanged.

## Explicit bypass — `onto bypass <change> --to <phase|archive> --reason <reason>`

This is an emergency operator command, surfaced through the dedicated
`/onto-bypass` slash command only after the user explicitly requests it. It accepts any
workflow phase (`open`, `design`, `build`, `verify`, or `close`) or `archive`;
it skips the normal transition, evidence, dependency, merge, and worktree
checks. The framework-install gate, change-name validation, readable valid
state, and filesystem safety checks still apply.

Every invocation requires a non-empty user reason and appends a versioned
`.onto/bypass.json` record with the timestamp, command, source/target, reason,
and skipped checks. `archive` moves the existing workspace without merging its
spec deltas or ADRs. It is not a successful close.

## Merging specs — `onto merge-deltas <change>`

Before archiving, the close phase merges the change's spec deltas into the
living specs with `onto merge-deltas`: a deterministic
RENAMED → MODIFIED → REMOVED → ADDED application, lint-checked,
**transactional** (writes nothing unless every delta merges clean), and
**receipt-bound and resumable** (it records the exact delta manifest and living
spec pre/post-images in `.onto/merge-receipt.json`). A changed delta, deleted
delta, or unexpected living-spec image fails closed instead of being replayed.
This replaces the
by-hand merge that was the workflow's most destructive step.

It shares the close-plan review gate: `merge-deltas` refuses while
`close_confirmed` is empty, so the specs never move before the plan is validated.

## Exiting — `onto close <change>`

Archives a change that has reached the `close` phase. Gates, in order:

1. Framework installed; valid name; state loads.
2. Phase **is `close`** (advance until it reaches close first).
3. Every cumulative artifact for the workflow exists.
4. **Close-evidence gate** — the tokens the workflow produces:
   - `close_confirmed` is non-empty — the close-plan review summary, required of
     **every** workflow including presets, **and**
   - `verify.result == pass`, **and**
   - `close.merged == true` with a matching merge receipt, **and**
   - for the **full** workflow only, **guides resolved**: `guides` is
      `updated` or `waived:<reason>`. The fix/tweak presets produce no
      guides, so they skip this; an empty or unknown workflow is treated as
      full, **and**
   - `integration` is `merge` or `pr`, recorded while the workspace is active,
     **and**
   - immutable `base_ref` and `base_branch` anchors are recorded.
5. **Dependencies resolved** — every tracked dependency is archived and has
   completed integration; legacy archives remain accepted.
6. **Clean, determinable scope** (same config-repo plus selected-repo rule as
   entering close).
 7. **No-clobber** — the dated archive target takes a numeric suffix on
    same-day name reuse.

On success it writes a pending `.onto/integration.json` with one entry per
repository in the change's scope (the config repository plus every selected
sibling), sets `archived: true` and `integration_required: true`, and moves the
workspace to `docs/changes/archive/<YYYY-MM-DD>-<name>/`. If the process stops
between the move and flag write, rerunning `onto close <change>` repairs the
newest dated archive. The archived change remains at derived phase `close`
while any repository's integration is pending. Close also refuses when commits
landed after the recorded verification pass that are not workflow bookkeeping
(source paths in the config repo; anything at all in a selected sibling) —
re-verify and record a fresh pass first.

**`onto complete-integration <change> [--repo <alias>] --receipt <receipt>`**
records the one-way post-archive result, one repository at a time. Use
`merge:<commit-sha>` naming the real `--no-ff` merge commit (the binary
verifies parents and base-branch reachability against git and canonicalizes
the id) or `pr:<https-url>` after opening a pull request. Repeating the same
receipt is idempotent; replacing it is refused. The derived phase becomes
`done` and dependencies resolve once **every** repository is complete. A
manual `ship.md` handoff is not completion because no PR has opened.

**`onto abandon <change>`** is the other terminal state — the unsuccessful
one — for work that stops rather than completes.

## Recording evidence — `onto set <field> <change> [value]`

Gate tokens live in `onto-state.yaml` and are set through `onto set`, never
by hand:

| `onto set` field | Gate it satisfies / records |
|---|---|
| `proposal-approved <change> <evidence>` | required to **leave open** (full only); proposal review and basis |
| `approach-confirmed <change> <evidence>` | required to **enter build** (full only); selected approach and basis |
| `close-confirmed <change> <evidence>` | required for **`merge-deltas`** and **`close`** (all workflows); close-plan review summary |
| `isolation <branch\|worktree>` | required to **enter build** |
| `integration <merge\|pr>` | required to **close**; how the branch is integrated after archive |
| `build-pause <plan-ready\|clear>` | record/clear an explicitly requested pause after planning so a fresh session resumes without re-planning |
| `verify-result <pass\|fail\|…>` | `pass` required to **leave verify** and to **close**; `fail` also increments `observed.verify_rounds` (≥3 is an `onto doctor` finding) |
| `verify-scale` | records the verification level for the verify phase (see `onto scale --set`) |
| `close-merged` | compatibility spelling that delegates to `onto merge-deltas`; it cannot set an unbound marker |
| `guides <updated\|waived:<reason>>` | required to **close** a full workflow |
| `deps --dep <name> …` | dependency list; each must be archived before **close** |
| `build-mode`, `tdd-mode` | records how build executes |
| `base-ref` | immutable commit anchor the change branched from (input to `onto scale`) |
| `base-branch` | immutable, syntax-checked branch targeted by local integration or a pull request; separate from commit-valued `base-ref` |
| `workflow <full>` | one-way preset upgrade from `fix`/`tweak` to the full workflow |
| `supersedes`, `deviates-from` | cross-change relationships (surfaced by `onto graph`) |
| `directive` | a verbatim pre-authorization directive on the change |

## Read-only inspection (no gates, config-independent)

| Command | What it reports |
|---|---|
| `onto status` | each active change's derived phase and skeleton validity |
| `onto state <change> [--json]` | a change's full state |
| `onto gate <change> [--json]` | pending evidence decisions and the exact command that resolves each; most use `onto set`, while delta merging uses `onto merge-deltas` |
| `onto scale <change> [--json] [--set]` | the verification level derived from the measured `base_ref..HEAD` diff (non-test files, changed lines); `--set` records it via `verify-scale` |
| `onto graph [--json] [--check]` | the change dependency graph (`{nodes, edges, cycles}`); `--check` exits non-zero on a cycle — the same cycles the build gate rejects |
| `onto dirt [change] [--json]` | Every uncommitted path classified against the change. For a scoped change it audits the config repo plus its selected aliases and labels every repository; config-repo `own` and other-change `change` classifications retain their existing meanings, while external paths are `source` and block close. |
| `onto handoff <change> [--write]` | a compact recovery context pack (identity, phase, pending gate, artifact excerpts + a content hash) for continuing after a context compaction; `--write` persists it under `docs/changes/<name>/.onto/handoff/` |
| `onto doctor [--quiet]` | workspace health across layout, state, phase/artifact match, dependency resolution, and archive layout; non-zero on any finding. Also reports **`tasks.md` ↔ `plan.md` drift** — a task number in one file and not the other, or a checkbox in `plan.md`, which breaks resuming from the first unchecked item (a change with no `plan.md` is a preset, not drift). Also reports **version skew** between the `onto` binary and the homonto that installed the framework (fix with `homonto update`), and ≥3 failed verify rounds. `--quiet` prints nothing and signals via exit code only — the hook primitive (see [enforcement](enforcement.md)) |
| `onto version` | the release-stamped version |

## Recovery packs — `onto handoff <change> [--json] [--write]`

`handoff` emits the recovery context: identity, phases (claimed and
derived), deps, repo aliases, commits, pending gates (as argv templates),
artifact digests, and a safe next command. `--json` prints the interactive
view (envelope plus the full state); `--write` persists the metadata-only
recovery view — versioned JSON and Markdown under
`docs/changes/<name>/.onto/handoff/` with create-only, no-follow, confined
writes. Persisted packs carry no artifact prose and no free-form state, so a
secret pasted into a plan cannot reach them (ADR 0027).

## Structured evidence — `onto evidence record` and `onto trace`

`onto evidence record <change> --task N --scenario <Scenario-ID> --exec <name>
--cmd-hash <sha256> --exit <n> [--output <file>]` records one verification
claim in `docs/changes/<name>/.onto/evidence.json`: hashes only, never argv
or output, anchored to the current commit. The binary never executes the
command — you run it, then record, so verification stays inside the host's
permission checks. `onto trace [change] [--json]` renders the typed graph:
changes, capabilities, requirements, scenarios, tasks, commits, and
evidence. `onto doctor` reports unknown scenarios/tasks, duplicate IDs,
unreachable commits, and changed verification artifacts; a change verified
without a sidecar gets a note, not a finding.

## Driving it from the tool — slash commands

`homonto apply` installs a slash command per phase and preset, so you can
drive the flow from the command palette: `/onto` (the dispatcher — it
derives the active change's phase and routes automatically), plus
`/onto-open`, `/onto-design`, `/onto-build`, `/onto-verify`, `/onto-close`,
`/onto-fix`, `/onto-tweak`, and `/onto-no-slop`. Each command loads its
matching skill; the binary still owns every state change.
