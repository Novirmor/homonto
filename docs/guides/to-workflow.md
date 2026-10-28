# The to workflow

`to` is the minimal engine: three phases, one artifact, and a small
bookkeeper binary. It keeps onto's coding standards — grounded plans,
reviewed diffs, real verification, an adversarial pass — without onto's
evidence gates, spec deltas, or dependency graph.

Use it for focused work where nobody needs an audit trail beyond a plan file
and Git history. Comparison: [workflows](workflows.md). Command details:
[to reference](to-reference.md). Design rationale:
[to-framework-design.md](../to-framework-design.md).

## Two halves

| | Owns |
|---|---|
| **`to` binary** | `to-state.yaml`, the one forward transition, archiving, status, handoff, doctor. |
| **`to-*` skills** | Planning, implementation loops, review, verification, delivery. |

The binary does **not** check verification: `to done --verified` records your
assertion. The rigor lives in the `to-done` skill, which requires a real
verification run and at least one completed skeptic pass.

## Install

```toml
[engines.to]
source = "builtin:to"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```sh
homonto apply
to init          # creates docs/tasks/ and docs/tasks/archive/
```

## The flow

```text
 to new ─▶ plan ──to phase──▶ do ──to done --verified──▶ done (archived)
            │                  │
            │                  └ tasks implemented, reviewed, committed, checked off
            └ plan.md written

 any phase ──to abandon──▶ abandoned (archived)
```

From OpenCode:

```text
/to cap the note cache at 100 entries
```

The coordinator runs plan, do, and done in one invocation, through
verification, archive, integration, and authorized publication, stopping only
for an endpoint you named, a real decision, or a hard blocker.

## The plan contract

`plan.md` is the change's single durable record — planning, decisions, review
outcomes, and final evidence all live here. Keep it under a screen.

```markdown
# tune-cache

Goal: cap the note cache at 100 entries.
Approach: evict the oldest entry on insert past the cap.
Boundary: no other eviction policy changes; no config knob.

- [ ] Cap cache size
  - Files: `cache.go` (`Cache.Put`), `cache_test.go`
  - Change: drop the oldest entry when the 101st is added
  - Verify: `go test ./... -run TestCacheCap` — ok
- [ ] Log evictions at debug level
  - Files: `cache.go`
  - Change: one debug line per eviction with the evicted key
  - Verify: `go test ./...` — ok

Final Verify: `go test ./... && go vet ./...` — both exit 0

## Notes
- Considered LRU; rejected: access order isn't tracked today.

## Verification
```

Rules:

- One concrete outcome per task. Code and its focused tests stay in the same
  task.
- Every task has `Files:`, `Change:`, and `Verify:` (exact command and passing
  signal). Multi-repository tasks also name `Repo:` and an absolute `Cwd:`.
- **The list is live during `do`.** Discovered work is appended with the same
  contract, suffixed `(discovered <date>)`, before `Final Verify:` and
  before its code is written.
- A task made unnecessary is checked as `- [x] SUPERSEDED: <reason>`, never
  deleted.
- Each task is checked off in the commit that completes it.
- Declined review findings and decisions go under `## Notes`.
- `## Verification` gets the literal `Final Verify:` result, coverage gaps,
  the skeptic verdict, and the verified commit and tree IDs.

More task examples ship in the `to-plan` skill's `references/task-examples.md`.

## Phase by phase

### plan (`/to-plan`)

The coordinator grounds the approach in the code (dispatching `to-explorer`
for multi-file questions), writes `plan.md`, de-slops it, and advances:

```console
$ to new tune-cache
created change "tune-cache" at docs/tasks/tune-cache
  docs/tasks/tune-cache/to-state.yaml
  docs/tasks/tune-cache/plan.md
$ to phase tune-cache
change "tune-cache" advanced plan → do
```

If planning starts growing design documents, cross-change dependencies, or
evidence tables, that's onto-shaped work — the skill says so rather than
rebuilding onto inside a plan.

### do (`/to-do`)

For each unchecked task:

1. `to-implementer` makes the change in **one repository** and runs the
   task's `Verify:`; it returns edits and evidence, never commits.
2. The coordinator re-runs the check against the repository.
3. One or more `to-reviewer` passes (distinct lenses, in parallel) judge the
   diff.
4. Findings are fixed or declined in writing (`## Notes`).
5. The coordinator commits the code and the checkoff together.

Independent repositories run in parallel; each checkout has one writer.

### done (`/to-done`)

1. Run `Final Verify:` on the final candidate (reusing earlier output only if
   inputs are unchanged).
2. Obtain at least one **completed** `to-skeptic` pass on that exact
   candidate, with a complete evidence pack. Several lenses may run
   concurrently: does it do what it claims, what breaks it, does the evidence
   hold. If dispatch is unavailable, a direct review is recorded as such —
   never labelled a skeptic pass.
3. Triage findings. An in-scope defect becomes a new task, and the flow goes
   back to `do` in the same invocation; any code change invalidates the
   previous verification and verdict.
4. Record the outcome under `## Verification`.
5. Finish:

```console
$ to done tune-cache --verified --evidence "go test ./... && go vet ./... ok; to-skeptic: no blocking findings"
change "tune-cache" done, archived at docs/tasks/archive/2026-10-27-tune-cache
```

6. Complete authorized integration and publication. An archived task may
   still owe a merge or PR; the dispatcher detects that on the next `/to` and
   resumes the first missing action without re-archiving.

## Specialists in to

| Agent | Role | Concurrency |
|---|---|---|
| `to-explorer` | Read-only codebase questions with cited conclusions | one per question |
| `to-implementer` | One repository assignment; returns edits and evidence | parallel across repos, serial per checkout |
| `to-reviewer` | Diff review for correctness, security, contract, clarity | one per lens |
| `to-skeptic` | Fresh-context attempt to refute "it works" | one per lens, ≥1 completed |

Bookkeeping — `plan.md` edits, checkoffs, commits — stays with the
coordinator and is never concurrent.

## Multi-repository changes

```console
$ to new release-2 --repo api --repo web --dir /work/control
```

State and `plan.md` live in the records root; tasks name their `Repo:` and
`Cwd:`. Before `to done`, every selected repository must be clean and its Git
state readable. Schema 2 adds frozen bases (`--base api=main`) and
registered worktrees. See [workspaces](workspaces.md).

A change created without `--repo` in a schema 0/1 config is Git-blind: `to
done` doesn't check cleanliness.

## Recovering

```console
$ to handoff tune-cache
change: tune-cache
phase: do
created: 2026-10-27
next: resume implementation with `/to-do` for tune-cache
next argv: ["to" "status" "--json" "--dir" "…"]

plan.md:
# tune-cache
…
```

The pack contains the plan head, every unchecked task contract, `Final
Verify:`, and bounded notes/verification. A fresh session resumes at the
first unchecked task.

An interrupted `to done` / `to abandon` leaves the change terminal but not
archived; `to doctor` reports it, and re-running the same finishing command
completes the move.

## Growing out of to

Engine choice is fixed per task. If a `to` change turns out to need onto's
record, the coordinator asks you for a scope decision. You can convert
explicitly:

```console
$ to promote tune-cache --yes
```

The whole to workspace is preserved under the new onto change's
`.workflow/snapshots/`, and a fresh proposal-only onto change starts at
`open`. `onto demote` converts back.

## Emergency bypass

Only when you ask, via `/to-bypass`:

```sh
to bypass tune-cache --to done --reason "released manually during outage"
```

`done` and `archive` record `verified: false`; every use is logged in
`.to/bypass.json`.
