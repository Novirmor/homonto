# onto reference

Every `onto` command, flag, and gate. Concepts and a worked example:
[onto workflow](onto-workflow.md).

## Conventions

- `--dir <config-root>` (default `.`) selects the directory containing
  `homonto.toml`. Pass the config root even when your shell is in a source
  worktree.
- `<workflow-root>` is `[workflow] root` (default `docs`). Changes live in
  `<workflow-root>/changes/<name>/`, archives in `changes/archive/`.
- Change names are kebab-case; path traversal is rejected; `archive` is
  reserved. Active names are unique across onto and to.
- **Install gate.** `init`, `new`, `set`, `advance`, `bypass`, `close`,
  `abandon`, `merge-deltas`, and `complete-integration` refuse unless `homonto.toml` declares `[engines.onto]` and
  `homonto apply` has materialized `.homonto/catalog/skills/onto`. `demote`
  also accepts an applied `[engines.to]`.
- Read-only commands need no install but still read `homonto.toml` for the
  records root and source scope.
- Output goes to stderr. Exit `0` on success, non-zero on a refused gate or
  error.
- Managed records (`workflow.git = "managed"`): each mutation checkpoints its
  own record changes. If a checkpoint fails after the state write, the command
  exits non-zero; run `homonto workspace recover`, then inspect state before
  retrying.

## Command summary

| Command | Writes | Purpose |
|---|---|---|
| `onto init` | yes | Create `changes/`, `specs/`, `adr/`, `guides/` (never overwrites). |
| `onto new <c>` | yes | Create a change at phase `open`. |
| `onto set <field> <c> …` | yes | Record a decision or evidence token. |
| `onto advance <c>` | yes | Move one phase forward if every gate passes. |
| `onto merge-deltas <c>` | yes | Merge delta specs into living specs. |
| `onto close <c>` | yes | Archive a change in phase `close`. |
| `onto complete-integration <c>` | yes | Record a merge, PR, or no-op receipt. |
| `onto abandon <c>` | yes | Unsuccessful terminal state. |
| `onto bypass <c>` | yes | User-requested emergency skip, audited. |
| `onto demote <c>` | yes | Convert into a `to` change. |
| `onto evidence record <c>` | yes | Append a structured verification claim. |
| `onto status` | no | Derived phase of each active change. |
| `onto state <c>` | no | Full state. |
| `onto gate <c>` | no | Pending decisions with resolving commands. |
| `onto scale <c>` | `--set` only | Measure the diff → verification level. |
| `onto dirt [c]` | no | Classify uncommitted paths. |
| `onto graph` | no | Dependency graph. |
| `onto trace [c]` | no | Requirements → scenarios → tasks → evidence. |
| `onto handoff <c>` | `--write` only | Recovery pack. |
| `onto doctor` | no | Workspace health. |
| `onto cleanup list\|preview\|apply` | `apply` only | Archive abandoned records and remove clean worktrees (branches kept) and scratch of terminal changes. |
| `onto version` | no | Release version. |

## `onto init`

```console
$ onto init
created docs/changes
created docs/specs
created docs/adr
created docs/guides
```

Idempotent: existing paths are reported as skipped.

## `onto new <change>`

```text
onto new <change> [--workflow full|light] [--repo <alias>]... [--base <alias>=<branch>]...
```

Creates `<workflow-root>/changes/<change>/` with `onto-state.yaml` (phase
`open`) and an empty `proposal.md`; presets also get `tasks.md`. Refuses an
existing name.

| Flag | Notes |
|---|---|
| `--workflow` | `full` (default) or `light`. |
| `--repo` | Repeatable `[repos]` alias. Schema 2: required, and the list is the complete source set. Schema 0/1: optional extra siblings beside the implicit config repository. |
| `--base` | Schema 2 only, repeatable, once per selected alias: integrate into this existing **local** branch, anchored at its current tip. Default: each source's current branch and HEAD. The dirty checkout is not switched. |

```console
$ onto new add-search --repo api --repo web --base api=main --base web=develop --dir /work/control
```

Anchors are frozen at creation; later worktree creation must match them.

## `onto advance <change> [--to build]`

Attempts one transition and writes nothing unless every check passes, in
this order:

1. Install gate, valid name.
2. State loads; change not abandoned.
3. The current phase has a next phase.
4. **Required artifacts** (cumulative):

   | Leaving | full | light |
   |---|---|---|
   | open | `proposal.md` | `proposal.md`, `tasks.md` |
   | design | + `design.md`, `tasks.md` | (pass-through) |
   | build | + `plan.md` | — |
   | verify | + `verification.md` | + `verification.md` |

5. **Leaving build:** no `- [ ]` left in `tasks.md`.
6. **Evidence tokens:**
   - leaving open (full): `proposal_approved` non-empty;
   - entering build: `isolation` set; `approach_confirmed` non-empty (full);
     every dependency complete; no dependency cycle;
   - leaving verify: `verify.result == pass` and exactly one canonical
     passing `Result:` line in `verification.md`.
7. **Entering close:** the selected source checkouts are clean and readable
   (see [cleanliness](#source-cleanliness)). Other transitions only warn
   about dirt.

`--to build` (presets only) walks the hops to build in one call; each hop is
gated and saved individually.

```console
$ onto advance add-search
error: onto advance: cannot leave open: proposal review not recorded (review the proposal, then run `onto set proposal-approved add-search "<evidence>"`)
$ onto advance fix-crash --to build
error: onto advance: cannot enter build: missing isolation (set branch or worktree)
```

A full change upgraded from an older onto that lacks a now-required review
token refuses its next advance until that review is recorded.

## `onto set <field> <change> [value]`

| Field | Values | Gate / meaning |
|---|---|---|
| `proposal-approved <c> "<evidence>"` | text | Leave open (full). |
| `approach-confirmed <c> "<evidence>"` | text | Enter build (full). |
| `isolation <c> <v>` | `branch`, `worktree` | Enter build. |
| `build-mode <c> <v>` | `direct`, `subagent` | Who edits during build. |
| `tdd-mode <c> <v>` | `tdd`, `direct` | Whether build writes the failing test first. |
| `build-pause <c> <v>` | `plan-ready`, `clear` | Record an explicitly requested pause after planning. |
| `verify-result <c> <v>` | `pending`, `pass`, `fail` | Leave verify, close. `pass` captures selected source HEADs; `fail` increments the round counter. |
| `verify-scale <c> <v>` | `light`, `full` | Verification level. |
| `guides <c> <v>` | `pending`, `updated`, `waived:<reason>` | Close (full). |
| `integration <c> <v>` | `merge`, `pr` | Close. |
| `close-confirmed <c> "<evidence>"` | text | `merge-deltas` and close (all workflows). |
| `close-merged <c>` | — | Compatibility alias that delegates to `merge-deltas`. |
| `base-ref <c> <ref> [--repo a]` | commit | Diff anchor for `scale`. Schema 2: validates the frozen per-repo anchor; can't rebase it. |
| `base-branch <c> <branch> [--repo a]` | branch | Integration target. Schema 2: validates the frozen target. |
| `deps <c> --dep <name>…` | change names | Must be complete before build/close. |
| `supersedes <c> --change <name>…` | change names | Shown in `onto graph`. |
| `deviates-from <c> --from <name>…` | names | Shown in `onto graph`. |
| `directive <c> "<text>"` | text | A verbatim pre-authorization from you. |
| `workflow <c> full` | `full` | One-way preset upgrade; invalidates build/verify/close evidence. |

## `onto merge-deltas <change>`

Merges `changes/<c>/specs/*.md` into `<workflow-root>/specs/` in a fixed
order (RENAMED → MODIFIED → REMOVED → ADDED), after linting. Transactional:
nothing is written unless every delta merges cleanly. Writes a receipt
(`.onto/merge-receipt.json`) with the delta manifest and living-spec pre/post
images so an interrupted merge resumes, and a changed delta or unexpected
spec fails closed. Refuses until `close-confirmed` is recorded.

Delta lint rules: only the four section headings; each ADDED/MODIFIED
requirement's first normative line contains SHALL or MUST; each has ≥1
scenario with WHEN and THEN bullets; MODIFIED/REMOVED/RENAMED names match the
living spec exactly.

## `onto close <change>`

Archives a change in phase `close`. Checks, in order:

1. Install gate, name, state.
2. Phase is `close`.
3. Every cumulative artifact exists.
4. Close evidence: `verify.result == pass` with one passing `Result:` line;
   `guides` `updated` or `waived:…` (full); `integration` set; valid base and
   target anchors; `close_confirmed`; `close.merged == true`.
5. The verified source is intact and the merge receipt matches.
6. Every dependency's integration is complete.
7. Any pending integration record from an interrupted attempt is valid.
8. Clean, determinable source scope.
9. No clobber: same-day name reuse gets a numeric suffix.

On success it writes `.onto/integration.json` (one pending entry per source
repository), marks the change archived, and moves it to
`changes/archive/<YYYY-MM-DD>-<c>/`. Rerunning after an interruption repairs
the newest archive.

Close refuses if commits after the recorded verification change source.
Every intervening commit is inspected, not only the net diff; in a combined
records/source checkout, record-only commits (`changes`, `specs`, `adr`,
`guides`) are allowed. Re-verify, or reopen for a real fix.

## `onto complete-integration <change>`

```text
onto complete-integration <change> --receipt <receipt> [--repo <alias>] [--head <sha>]
```

| Receipt | Proves |
|---|---|
| `merge:<sha>` | A real two-parent merge reachable from the recorded base branch, whose first parent descends from the captured base and whose second parent is the verified candidate (or a records-only descendant in a combined checkout). Squash, rebase, and fast-forward merges don't qualify. |
| `unchanged:<sha>` | A proven no-op: the receiving commit descends from the base and is on the target branch, and the source was already contained (or tree-identical) at capture. |
| `pr:<https-url>` | An opened PR. Explicit sources also need `--head <40/64-hex sha>` from the publication tool, checked locally against the candidate. Not verified with the host. |

`--repo` is required when several explicit sources are selected (one is
inferred; legacy defaults to the config repository). Repeating the same
receipt is idempotent; replacing it is refused. The change derives `done`
when every repository is complete.

```console
$ onto complete-integration add-search --receipt merge:$(git rev-parse HEAD)
add-search: integration complete for repository config (merge:b7e101e…)
$ onto complete-integration add-search --repo api --receipt pr:https://github.com/acme/api/pull/42 --head 3f2a…
```

## `onto abandon <change>`

Marks the change abandoned — the unsuccessful terminal state. The record stays
in `changes/<change>/` and keeps reserving the name until
[`onto cleanup`](#onto-cleanup) archives it:

```console
$ onto abandon spike-x
spike-x: abandoned (was at phase open)
$ onto new spike-x
error: onto new: change "spike-x" already exists at …/docs/changes/spike-x; resume it, or inspect terminal cleanup with `onto cleanup preview spike-x --dir "…"`
```

After cleanup, the name is free again. An abandoned archive never blocks a new
change of the same name; an archive whose integration is incomplete still does.

## `onto bypass <change> --to <phase|archive> --reason "<reason>"`

User-requested only (the `/onto-bypass` command). Target is `open`, `design`,
`build`, `verify`, `close`, or `archive`. Skips transition, evidence,
dependency, merge, and worktree checks; still requires the install gate, a
valid name and state, and filesystem safety. Appends
`.onto/bypass.json` with timestamp, command, source, target, reason, and
skipped checks. `archive` moves the workspace without merging deltas or ADRs.

## `onto demote <change> [--as <name>] --yes`

Converts an active onto change into a `to` change. The whole workspace is
preserved under `.workflow/snapshots/` of the new change.

- From open/design: the to change starts at `plan`.
- From build/verify: continues at `do` only if every dotted task has a
  matching `## Task N.M` contract with concrete `Owner:`, `Repo:`, absolute
  `Cwd:`, `Files:`, `Do:`, and `Verify:` fields (`Do:` becomes `Change:`;
  `Final Verify:` is carried or taken from the last task). Otherwise restarts
  at `plan` with a stub — no invented detail.
- `DEFERRED TO CLOSE:` tasks transfer unchecked.
- Closed, abandoned, or archived changes are refused, as are changes with
  registered worktree bindings.
- `to promote` back without edits restores the original byte-for-byte.
- Crash recovery is receipt-verified; tampered staging is refused.

## `onto evidence record <change>`

```text
onto evidence record <change> --task N --scenario <Scenario-ID> --exec <name>
    --cmd-hash <sha256> --exit <n> [--repo <alias>] [--output <file>] [--artifact <file>]
```

Appends one claim to `.onto/evidence.json`: the command is named and hashed,
the output file hashed — never stored — and the claim is anchored to the
checkout's current commit. **onto never runs the command**; run it yourself
(so your permission rules apply), finalize `verification.md`, then record.

- `--task` is the numeric trace ID from `[trace #N]`.
- `--scenario` must be declared exactly once: in a delta spec, or — when the
  change has no delta files — on its own line in `tasks.md` or
  `verification.md`. Unknown or duplicate IDs are rejected.
- `--artifact` defaults to `verification.md`, whose hash makes a claim stale
  if the report later changes.
- The last claim per (repo, task, scenario) supersedes earlier ones; history
  is kept.

```console
$ onto evidence record add-search --task 1 --scenario SC-add-search-match --exec go \
    --cmd-hash "$(printf 'go test ./...' | sha256sum | cut -d' ' -f1)" --exit 0 --output test.out
recorded SC-add-search-match <- task 1, go, exit 0, commit 10346c35645a
```

## Inspection commands

### `onto status`

```console
$ onto status
add-search: open — skeleton ok
fix-crash: build — skeleton ok
```

Archived changes with pending integration show as
`add-search: close (archived; integration pending)`.

### `onto state <change> [--json]`

The full state. JSON includes `derived_phase` (computed from files) and
`phase_mismatch` when the recorded phase disagrees.

### `onto gate <change> [--json]`

Pending decisions, each with options and the command that resolves it:

```console
$ onto gate add-search
[Approach] Has the design approach been selected and its basis recorded?
  → onto set approach-confirmed add-search "<evidence>"
[Isolation] How should this change be isolated for build?
  * branch — A branch off the base ref
    worktree — A separate git worktree (parallel work / dirty tree)
  → onto set isolation add-search <value>
```

### `onto scale <change> [--json] [--set]`

```console
$ onto scale add-search
add-search: 1 non-test file(s), 11 changed line(s) → verify-scale light
$ onto scale add-search --json
{"files":1,"lines":11,"level":"light","recorded":false}
```

Measures committed non-test changes since `base-ref` (schema 2: each
selected checkout against its frozen base). More than five non-test files →
`full`. `--set` records the level.

### `onto dirt [change] [--json]`

```console
$ onto dirt fix-crash
?? docs/changes/fix-crash/notes.md (own)
?? docs/changes/other-change/proposal.md (change)
 M main.go (source)
3 uncommitted path(s), 2 blocking close
```

Without a change, reports config-checkout dirt.

### `onto graph [--json] [--check]`

The dependency graph as `{nodes, edges, cycles}`. `--check` exits non-zero
on a cycle.

### `onto trace [change] [--json]`

The typed graph of changes, capabilities, requirements, scenarios, tasks,
commits, and evidence, with `superseded-by` edges between attempts.

### `onto handoff <change> [--json] [--write]`

Identity, claimed and derived phase, dependencies, repositories, commits,
pending gates as argv, artifact digests and excerpts, and a safe next
command. `--write` persists a metadata-only pack (no prose) under
`.onto/handoff/`.

### `onto doctor [--quiet]`

Workspace health; non-zero on any finding: missing layout, malformed state,
phase/artifact mismatch, unresolved dependencies, archived-but-active
changes, `tasks.md`/`plan.md` drift, malformed archives, ≥3 failed verify
rounds while still failing, stale or unknown evidence, duplicate scenario IDs
(with paths and lines), and version skew between `onto` and the homonto that
installed the skills (fix with `homonto update`). `--quiet` prints nothing
and reports only by exit status.

## `onto cleanup`

```text
onto cleanup list    [--json] [--dir <root>]
onto cleanup preview <change> [--identity <id>] [--json] [--dir <root>]
onto cleanup apply   <change> --identity <id> --yes [--json] [--dir <root>]
```

Cleans up after a change that is over: archived with every integration
receipt, or abandoned. It archives an abandoned record still in `changes/` and
removes registered worktrees that are clean and integrated. It keeps branches
and never edits archives. It doesn't need `[engines.onto]` to be installed.
`list` and `preview` write nothing. `apply` needs the exact `--identity` from
`list` or `preview`, and `--yes` confirms without bypassing any check:

```console
$ onto cleanup preview spike-x
onto/spike-x	cleanup-pending	identity=13a7e345	changes/spike-x
  archive: changes/archive/2026-10-27-spike-x
  apply: onto cleanup apply spike-x --identity 13a7e345 --yes
$ onto cleanup apply spike-x --identity 13a7e345 --yes
onto/spike-x	cleanup-complete	identity=13a7e345	changes/archive/2026-10-27-spike-x
```

Statuses, blockers, receipts, and retrying:
[cleaning up terminal workflows](workflow-cleanup.md).

## Source cleanliness

Entering close and closing audit the change's **recorded** source scope:

- Schema 2: only the selected aliases, using each registered execution
  worktree when present.
- Schema 0/1: the config repository plus selected siblings.

In a checkout that holds both records and source, paths are classified `own`
(blocks), `change` (doesn't), or `source` (blocks). A separate source
checkout has no records carve-out. Missing aliases, invalid bindings, or
unreadable Git state block rather than pass.

## Task and trace IDs

```markdown
- [ ] 1.1 Implement parser [trace #1]          ← tasks.md
## Task 1.1 — Implement parser                  ← plan.md
```

The dotted ID binds `tasks.md` to `plan.md` (doctor checks the match). The
`[trace #N]` ID binds evidence and trace output. Legacy `- [ ] #1 …` lines
still work; `fixes #1` in prose doesn't create a trace task.

## State file

`onto-state.yaml` (schema 3) is written only by the binary:

```yaml
schema_version: 3
change: add-search
id: 7984967a
workflow: full
phase: open
created: "2026-10-27"
```

Sidecars live in the change's `.onto/` directory: `evidence.json`,
`merge-receipt.json`, `integration.json`, `bypass.json`, `handoff/`.
