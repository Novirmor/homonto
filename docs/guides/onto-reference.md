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

Most commands take `--dir <root>` (default `.`) to select the configuration
root. `<workflow-root>` below means `[workflow].root` from its
`homonto.toml` (default `docs`). Phase/lifecycle mutations require the onto framework
to be installed by homonto, except `demote`, which bridges the frameworks
and accepts either an applied `[frameworks.onto]` or an applied
`[frameworks.to]`. Read-only commands do not write; `onto dirt <change>` reads
configuration to resolve the records root and recorded source scope. Read-only
does not mean config-independent; legacy config-free recovery uses `docs`.

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
`<workflow-root>/changes/<name>/onto-state.yaml`. The phase set is exactly
`open → design → build → verify → close`; `close` is the terminal phase
(reached by advancing), after which `onto close` archives the change and records
pending Git integration. `done` is derived only after `onto
complete-integration` records a merge, proven no-op, or opened-PR claim.
There is no `archive` phase.

## Entering — `onto init` and `onto new`

**`onto init [--dir <root>]`** scaffolds the `<workflow-root>/{changes,specs,adr,guides}/`
layout, idempotently. It reports created vs. skipped paths and never
overwrites existing content.

**`onto new <name> [--workflow full|fix|tweak] [--repo <declared-name>] [--base <alias>=<branch>]`** creates
`<workflow-root>/changes/<name>/` with:

- `onto-state.yaml` at **phase `open`**, `workflow: full` (the default);
- a `proposal.md` skeleton — plus `tasks.md` **only for the fix/tweak
  presets**; a full change's `tasks.md` is derived later, in design.

It requires the framework installed, refuses to clobber an existing change,
and validates that the name is kebab-case with no path traversal.

`--repo` is repeatable and records selected names from `[repos]` in
`onto-state.yaml`. Schema 2 requires at least one alias and has no implicit
config source; schema 0/1 retains the implicit config repo and optional
additional aliases. Records stay at `<workflow-root>`. Pass the config root
as `--dir`, even from a source worktree.

Schema 2 also accepts repeatable `--base <alias>=<branch>`, once per selected
alias, naming an existing local branch. Without it, creation captures each
source's current HEAD and attached local branch. An override captures the
chosen branch's tip and integration target without switching or cleaning the
source checkout:

```bash
onto new add-search --repo api --repo web --base api=main --base web=develop --dir /work/control
```

Choose these anchors before record creation. Worktree creation must match both
the frozen commit and target; later setters cannot rebase or retarget them.
Schema 0/1 rejects `--base`. A retained worktree binding blocks reuse of its
workflow/change name even after archive or abandonment. See
[workspaces](workspaces.md) for isolation and removal.

## Advancing — `onto advance <change> [--to build]`

Each call attempts **one** transition and writes nothing unless every gate
below passes, in this order. `--to build` is **preset-only** and the only
accepted value: it walks the gated advances up to build in one call, so a
`fix`/`tweak` change reaches build without two ceremonial advances. Every gate
below still applies to each step it walks. Each successful hop is saved; a later
failure does not roll back an earlier hop.

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

   An empty legacy workflow is treated as full; an unknown workflow is invalid.

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
    - **Leaving `verify`** (verify→close): `verify.result == pass` and exactly
      one canonical `Result:` line in `verification.md` reporting a pass.

   These review tokens preserve the decision across sessions: the binary refuses
   the transition while a token is empty. The coordinator records a truthful
   review summary; Git records who wrote it. Presets are exempt from the two
   full-only tokens but still record close-plan validation.
7. **Worktree cleanliness:** entering `close` audits the recorded source scope.
   Schema-2 explicit records select only their `--repo` aliases, using each
   registered execution worktree when present. Legacy records also include the
   implicit config repo. A real combined source/records checkout classifies its
   own change artifacts as `own` (blocking), other changes and archives as
   `change` (nonblocking), and other paths as `source` (blocking). A separate
   source checkout has no records carve-out, even for similarly named paths.
   Independent records Git and unselected sources are not extra source gates.
   A missing alias, invalid binding, unavailable Git worktree, or undeterminable
   result blocks. `onto dirt <change>` labels every repository and path.
   Other transitions only warn on determinable config-checkout dirt.

A failed gate exits non-zero without advancing that hop. In managed mode,
pending history recovery can also make the command fail after a phase write;
inspect state and use `homonto workspace recover` before retrying the workflow.

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

## Demotion — `onto demote <change> [--as <name>] --yes`

Converts an onto change into a `to` change (ADR 0042), the mirror of
`to promote` (ADR 0028): the complete source workspace moves unchanged into
the neutral control plane (`.workflow/snapshots/`) of a fresh change.
open/design restart at phase `plan`; build/verify continue at phase `do`
only when each dotted task has a matching `## Task N.M` plan contract with
concrete `Owner:`, `Repo:`, absolute `Cwd:`, `Files:`, `Do:`, and `Verify:`
fields. Translation preserves execution scope and multiline fields/checks,
renames `Do:` to `Change:`, and carries an explicit `Final Verify:` (including
continuations), or the final task's recorded check when no aggregate is present.
Tasks titled `DEFERRED TO CLOSE:` (case-insensitive prefix) transfer **unchecked**,
even if onto checked them to defer work; other checkboxes retain their state.
Missing, placeholder, or ambiguous contracts restart at `plan` with an honest
snapshot-based stub, not invented execution detail or verification. A
closed, abandoned, or archived change is refused. Converting back with
`to promote` while nothing changed restores the previous workspace
byte-for-byte; after edits it is a fresh conversion that snapshots the edited
bytes. Crash recovery is receipt-verified and idempotent, tampered staging is
refused, and the locks are taken in promote's fixed order (plus the onto
workspace lock, which excludes concurrent onto mutations of the source).

Conversion carries `repos`, `repo_mode`, and `repo_bases` source provenance,
including canonical Git identity and any recorded base/target anchors, rather
than recapturing today's HEAD. Current onto-state schema is 3 and to-state
schema is 1; these are separate from config schema 2. Legacy provenance may
have optional anchors; explicit source anchors remain required.

Registered worktree bindings block conversion: there is no ownership-safe
active-binding conversion or rebind command. Continue in the current workflow;
deleting a registry entry is not a supported workaround.

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
4. **Close evidence:** `verify.result == pass` and exactly one canonical passing
   `Result:` line in `verification.md`; guides `updated` or `waived:<reason>` for
   full workflows (presets are exempt); `integration` set to `merge` or `pr`;
   valid immutable base/target anchors (per selected alias in explicit mode,
   scalar `base_ref` / `base_branch` in legacy mode); non-empty `close_confirmed`
   for every workflow; and `close.merged == true`.
5. **Verified source intact** and a matching completed spec-merge receipt.
6. **Dependencies resolved:** every tracked dependency has completed archive
   integration; legacy archives remain accepted.
7. **Pending integration record valid**, if an interrupted attempt left one.
8. **Clean, determinable source scope** (same recorded-scope rule as entering
   close, with recovery of the command's own pending integration sidecar).
9. **No-clobber:** the dated archive target takes a numeric suffix on same-day
   name reuse; filesystem safety checks still apply.

On success it writes a pending `.onto/integration.json` with one entry per
repository in the change's source scope (only selected aliases in explicit
mode; config plus selected siblings in legacy mode), sets `archived: true` and
`integration_required: true`, and moves the
workspace to `<workflow-root>/changes/archive/<YYYY-MM-DD>-<name>/`. If the process stops
between the move and flag write, rerunning `onto close <change>` repairs the
newest dated archive. The archived change remains at derived phase `close`
while any repository's integration is pending. Close also refuses when commits
landed after the recorded verification pass that change source. For explicit
sources, the verified commit must remain an ancestor, and only a real combined
checkout allows descendants changing records under `changes`, `specs`, `adr`,
or `guides`. Every intervening commit is inspected, not just the net diff;
source edit/revert pairs and merge-parent source changes require re-verification.
A separate source must retain the exact verified candidate.

In managed mode, archive destination/date and source-state identity are prepared
before history intent, then reused by the handler. Source identity and target
safety are checked again before the move; the handler does not choose a different
archive date or suffix after gates. If history is pending after interruption,
recover it first and inspect state before retrying `onto close`.

**`onto complete-integration <change> [--repo <alias>] --receipt <receipt> [--head <canonical-commit>]`**
records the one-way post-archive result, one repository at a time. `--repo` is
required for multiple explicit sources; a single explicit alias is inferred.
Legacy omission selects config Git.

| Receipt | Contract |
|---|---|
| `merge:<commit-sha>` | In merge mode, a real two-parent merge reachable from the recorded local base branch. Its first parent descends from the captured integration base; its second parent is the pinned source candidate, or a permitted combined-layout bookkeeping-only descendant. Mere ancestry of the candidate is insufficient. The ID is canonicalized. |
| `unchanged:<receiving-commit-sha>` | A proven no-op in either integration mode. The receiving commit must descend from the captured integration base and be reachable from the recorded local target branch. At capture, the source must already have been an ancestor of that base, or descend from it with an identical tree. A merge that happened only afterward cannot manufacture this proof. No synthetic merge or empty PR is needed. |
| `pr:<https-url>` | In PR mode, record the opened PR and, for explicit sources, `--head` with the canonical 40- or 64-character lowercase commit ID reported by the publication tool. The head is checked locally against the pinned candidate with the same bookkeeping-only allowance. The URL/head is an **external assertion**, not remote attestation: onto does not query the host to prove publication. |

Repeating the same receipt is idempotent; replacing it or its publication head
is refused. Explicit receipts are revalidated even on retry. The derived phase
becomes `done` and dependencies resolve once **every** source is complete. A
manual `ship.md` handoff alone is not completion. Prefer allocating a needed
receiver early; identity-checked terminal/archive allocation also supports
post-archive recovery, with optional `--state-id` to disambiguate archives,
never to override existing bindings. See [workspaces](workspaces.md#allocate-an-integration-receiver).

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
| `verify-result <pending\|pass\|fail>` | `pass` captures selected source HEADs and is required to **leave verify** and to **close**; `fail` increments `observed.verify_rounds`. At least 3 rounds is a doctor finding only while the current result is `fail`; a later pass retains the historical count without that finding. |
| `verify-scale` | records the verification level for the verify phase (see `onto scale --set`) |
| `close-merged` | compatibility spelling that delegates to `onto merge-deltas`; it cannot set an unbound marker |
| `guides <updated\|waived:<reason>>` | required to **close** a full workflow |
| `deps --dep <name> …` | dependency list; each must be archived before **close** |
| `build-mode`, `tdd-mode` | records how build executes |
| `base-ref [--repo <alias>]` | Immutable commit anchor used by `onto scale`. In explicit mode, validates the selected alias's frozen `repo_bases` value without rebasing it; legacy mode uses scalar `base_ref`. |
| `base-branch [--repo <alias>]` | Immutable, syntax-checked integration target. In explicit mode, validates the selected alias's frozen target without retargeting; legacy mode uses scalar `base_branch`. Separate from commit-valued `base-ref`. |
| `workflow <full>` | one-way preset upgrade from `fix`/`tweak` to the full workflow |
| `supersedes`, `deviates-from` | cross-change relationships (surfaced by `onto graph`) |
| `directive` | a verbatim pre-authorization directive on the change |

## Inspection (no install gate)

These commands resolve the configured records root and, where needed, source
scope. `scale --set` and `handoff --write` are explicit write variants, not
read-only inspection.

| Command | What it reports |
|---|---|
| `onto status` | each active change's derived phase and skeleton validity |
| `onto state <change> [--json]` | a change's full state |
| `onto gate <change> [--json]` | pending evidence decisions and the exact command that resolves each; most use `onto set`, while delta merging uses `onto merge-deltas` |
| `onto scale <change> [--json] [--set]` | Verification level from committed source diffs (non-test files, changed lines). Explicit mode aggregates each selected execution checkout against its frozen base; legacy mode measures config Git against scalar `base_ref`. Actual combined-layout record trees are excluded. `--set` records the level via `verify-scale`. |
| `onto graph [--json] [--check]` | the change dependency graph (`{nodes, edges, cycles}`); `--check` exits non-zero on a cycle — the same cycles the build gate rejects |
| `onto dirt [change] [--json]` | Every uncommitted path classified against the change's recorded source scope, with repository labels and the combined-layout carve-out described above. Without a change, inspects config-checkout dirt rather than inventing a selected source set. |
| `onto handoff <change> [--write]` | a compact recovery context pack (identity, phase, pending gate, artifact excerpts + a content hash) for continuing after a context compaction; `--write` persists it under `<workflow-root>/changes/<name>/.onto/handoff/` |
| `onto doctor [--quiet]` | Workspace health across layout, state, phase/artifact match, dependencies, archives, and structured evidence; non-zero on any finding. Reports **`tasks.md` / `plan.md` drift** (mismatched task numbers or checkboxes in the plan; an absent preset plan is not drift), **version skew** (run `homonto update`), and at least 3 failed verify rounds only while `verify.result` remains `fail`. Historical failures resolved by a pass are not an unresolved finding. `--quiet` uses exit status only; see [enforcement](enforcement.md). |
| `onto version` | the release-stamped version |

## Recovery packs — `onto handoff <change> [--json] [--write]`

`handoff` emits the recovery context: identity, phases (claimed and
derived), deps, repo aliases, commits, pending gates (as argv templates),
artifact digests, and a safe next command. `--json` prints the interactive
view (envelope plus the full state); `--write` persists the metadata-only
recovery view — versioned JSON and Markdown under
`<workflow-root>/changes/<name>/.onto/handoff/` with create-only, no-follow, confined
writes. Persisted packs carry no artifact prose and no free-form state, so a
secret pasted into a plan cannot reach them (ADR 0027).

## Structured evidence — `onto evidence record` and `onto trace`

`onto evidence record <change> --task N --scenario <Scenario-ID> --exec <name>
--cmd-hash <sha256> --exit <n> [--repo <alias>] [--output <file>] [--artifact <file>]` records one verification
claim in `<workflow-root>/changes/<name>/.onto/evidence.json`: hashes only, never argv
or output, anchored to the selected execution checkout's current commit.
`--repo` is required for multiple explicit sources; a single alias is inferred,
while legacy omission selects config Git. The binary never executes the
command: run the checks, **finalize `verification.md` before recording claims**,
then record their real outcomes. The report is hashed by default; subsequent
edits make its current claims stale: re-verify and append replacements rather
than deleting the old attempts. `--artifact` selects another file to hash,
but doctor still compares the stored artifact hash to `verification.md`.

Evidence is append-only. The last appended claim for each **(repo, task,
scenario)** key supersedes earlier claims for an unambiguous scenario, even if
its command changes; timestamps do not determine precedence. Doctor checks current claims for stale tasks,
scenarios, commits, and report hashes, without making superseded failures a
permanent blocker. `onto trace [change] [--json]` retains audit history and adds
`superseded-by` edges. It renders the typed graph:
changes, capabilities, requirements, scenarios, tasks, commits, and
evidence. `onto doctor` reports unknown scenarios/tasks, duplicate scenario,
requirement, or operation IDs, unreachable commits, and changed verification
artifacts; a change verified
without a sidecar gets a note, not a finding.

For `fix`/`tweak` changes with **no delta specs**, declare each scenario on its
own standalone line at **one canonical location**, in either `tasks.md` or
`verification.md`, never both for the same ID, for example `Scenario-ID: cache-hit`.
These IDs are the preset's trace contract; do not invent a spec just to record
evidence. Full changes and presets with deltas use their delta-spec scenarios.

Scenario declarations must be unique across the change's contract. Doctor
reports duplicates with every declaration's relative path and line number,
even without an evidence sidecar. `evidence record` refuses an ambiguous ID;
trace retains existing audit records and reports the ambiguity but gives them
no coverage or `superseded-by` edges for that ID. Re-recording cannot choose
between two obligations that share an identifier.

Use plain mentions such as `cache-hit` or `See Scenario-ID: cache-hit` elsewhere.
Prose references, inline code, blockquotes, and fenced examples are not
declarations. In delta specs, declarations belong under a scenario within a
requirement. Related rationale: [ADR 0053](../adr/0053-keep-history-without-blocking-fresh-verification.md).

`--task N` takes the numeric marker from a task line such as
`- [ ] 1.1 Implement parser [trace #1]`. The dotted `1.1` must match the
corresponding `## Task 1.1` plan heading; `N` is the unique positive trace ID
for evidence and graph links. A legacy `- [ ] #1 ...` task remains supported,
but bare issue references such as `fixes #1` do not create trace tasks.

## Driving it from the tool — slash commands

`homonto apply` installs a slash command per phase and preset, so you can
drive the flow from the command palette: `/onto` (the dispatcher — it
derives the active change's phase and routes automatically), plus
`/onto-open`, `/onto-design`, `/onto-build`, `/onto-verify`, `/onto-close`,
`/onto-fix`, `/onto-tweak`, and `/onto-no-slop`. Each command loads its
matching skill; the binary still owns every state change.
