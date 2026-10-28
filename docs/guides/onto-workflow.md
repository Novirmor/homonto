# The onto workflow

onto is the evidence-gated engine: a change moves through five phases, and
the `onto` binary refuses each transition until the required artifacts and
recorded decisions exist. It leaves an archived record a stranger can read —
what was proposed, decided, built, verified, and how it was integrated.

Use it for work someone else has to resume, review, or audit. For lighter
work see [to](to-workflow.md); for choosing, see [workflows](workflows.md).
Every command and gate in detail: [onto reference](onto-reference.md).

## Two halves

| | Owns | Never does |
|---|---|---|
| **`onto` binary** | State (`onto-state.yaml`), transition gates, spec merging, archiving, integration receipts, evidence records | Judge quality, run your tests |
| **`onto-*` skills** | The work inside each phase: questions, design, code, review, verification | Edit state by hand |

The binary defends against a forgetful or rushed agent, not a malicious one:
it checks that evidence *exists and is shaped right*; the skills and the
skeptics check that it is *true*.

## Install

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```sh
homonto apply
onto init        # creates docs/{changes,specs,adr,guides}
```

Mutating `onto` commands refuse until `[engines.onto]` is declared **and**
applied (the install gate). Read-only commands always work.

## The flow

```text
 onto new ─▶ open ─▶ design ─▶ build ─▶ verify ─▶ close ─▶ onto close ─▶ archived ─▶ onto complete-integration ─▶ done
              │        │         │        │         │        (integration pending)
              │        │         │        │         └ guides, integration mode, close plan, merged specs
              │        │         │        └ verification.md, verify-result pass, clean tree
              │        │         └ plan.md, every task checked
              │        └ design.md, tasks.md, approach-confirmed, isolation
              └ proposal.md, proposal-approved

 presets (fix / tweak):  open-lite ─▶ build ─▶ verify ─▶ close     (design skipped)
 failure:                onto abandon <change>  → abandoned
 afterwards:             onto cleanup preview|apply <change>  → archive, remove worktrees
```

Each phase's requirements **accumulate**: leaving verify still needs the
proposal, design, tasks, and plan.

| Leaving | full workflow needs | fix / tweak need |
|---|---|---|
| open | `proposal.md`, `proposal-approved` | `proposal.md`, `tasks.md` |
| design | + `design.md`, `tasks.md`, `approach-confirmed`, `isolation`, deps complete, no dependency cycle | (pass-through) + `isolation` |
| build | + `plan.md`, every `tasks.md` box checked | every box checked (no `plan.md`) |
| verify | + `verification.md` with one passing `Result:` line, `verify-result pass`, clean source tree | same |

`onto gate <change>` always tells you what's missing and the exact command
that records it.

## Phase by phase

### open — what and why

The coordinator clarifies the request, checks whether it should be split into
several changes, and creates the workspace:

```console
$ onto new add-search
created change "add-search" at …/docs/changes/add-search
```

It writes `proposal.md`:

```markdown
# Proposal: add search

## Why
Users cannot find notes once they have more than a screenful.

## Scope
Add a `notes search <query>` command that lists notes containing the query.

## Out of scope
Ranking, fuzzy matching, an index.
```

…reviews it against your request and the code, and records that review — the
binary won't leave `open` without it:

```console
$ onto set proposal-approved add-search "proposal matches the request; scope excludes ranking"
$ onto advance add-search
add-search: open → design
```

### design — how, and proof it will work

No implementation code here. The coordinator grounds itself in the codebase
(explorers in parallel), compares 2–3 approaches, picks one from evidence and
your constraints, and writes:

**`design.md`** — the approach and why not the alternatives:

```markdown
# Design: add search

Status: Confirmed

## Approach
Linear scan over loaded notes with `strings.Contains`.

## Alternatives
- Inverted index — rejected: <1k notes, adds persistence.
```

**Delta specs**, one file per capability, `specs/<capability>.md`. Every
requirement has a SHALL/MUST line and testable scenarios with stable IDs:

```markdown
# Delta Spec: search (add-search)

## ADDED Requirements

### Requirement: Search notes by substring

Requirement-ID: REQ-add-search-1

The CLI SHALL list notes whose body contains the query.

#### Scenario: match found

Scenario-ID: SC-add-search-match

- **GIVEN** a note containing "milk"
- **WHEN** the user runs `notes search milk`
- **THEN** that note is listed
```

Sections are `ADDED`, `MODIFIED`, `REMOVED`, and `RENAMED Requirements`.

**ADR drafts** for decisions worth keeping (unnumbered, `Status: Proposed`).

**`tasks.md`** — the checklist, derived from the design. Each task has a
dotted plan ID and a numeric trace ID:

```markdown
- [ ] 1.1 Add search function with unit test [trace #1]
- [ ] 1.2 Wire `notes search` subcommand [trace #2]
```

Then the approach and the isolation choice are recorded:

```console
$ onto set approach-confirmed add-search "linear scan chosen over index: <1k notes"
$ onto set isolation add-search branch          # or: worktree
$ onto advance add-search
add-search: design → build
```

### build — one task, one commit

`plan.md` gives each task its detail under a matching heading:

```markdown
## Task 1.1 — Add search function with unit test
- Files: `search.go`, `search_test.go`
- Do: `search(notes, q)` returns notes containing q
- Verify: `go test ./...` — ok
```

For each task: an implementer edits and runs the check, reviewers judge the
diff (one per lens, in parallel), findings are fixed or declined in writing,
and the task's checkbox is ticked **in the same commit** as its code. So the
first unchecked box is always the resume point.

Rules the skill enforces:

- Discovered work is appended to `tasks.md` and `plan.md` **before** it is
  coded. No unnamed "while I'm here" changes.
- A failure gets root-cause debugging, not retry-until-green.
- Build work is never committed unisolated (that's why `isolation` gates
  entry).
- `onto set build-mode <change> direct|subagent` records who edits: the
  coordinator directly, or implementers.

```console
$ onto advance add-search
error: onto advance: cannot leave build: tasks.md has unchecked items
```

### verify — try to prove it wrong

```console
$ onto scale add-search
add-search: 1 non-test file(s), 11 changed line(s) → verify-scale light
```

`scale` measures the committed diff since `base-ref`. The skill then applies
risk overrides — full verification for the full workflow, upgraded presets,
new capabilities, or **any security-sensitive surface** (secrets, remote
fetch, deletion, permissions) regardless of size.

Then:

1. Every scenario is checked with **fresh** command output.
2. The regression suite runs.
3. At least **two skeptics** run in parallel from fresh context — a
   *conformance* lens (refute each scenario's evidence) and a *robustness*
   lens (attack what the scenarios don't cover) — prompted to refute, never
   approve. They have no shell; they request probes the coordinator runs.
4. The coordinator triages each finding: repair (appended as a task, back to
   build), evidence probe, or justified dismissal.
5. `verification.md` is written with exactly one `Result:` line:

```markdown
# Verification: add-search

Result: pass

## SC-add-search-match
`go test ./...` → exit 0, `ok example.com/notes 0.004s`

## Skeptics
- conformance: no findings
- robustness: empty query lists every note — accepted, documented
```

6. Structured evidence is recorded (hashes only; the binary never runs the
   command) and the result is set:

```console
$ onto evidence record add-search --task 1 --scenario SC-add-search-match \
    --exec go --cmd-hash "$(printf 'go test ./...' | sha256sum | cut -d' ' -f1)" \
    --exit 0 --output test.out
recorded SC-add-search-match <- task 1, go, exit 0, commit 10346c35645a
$ onto set verify-result add-search pass
$ onto advance add-search
add-search: verify → close
```

`verify-result fail` increments a round counter; three or more failed rounds
(while still failing) is a doctor finding that asks for a human decision.

### close — land the knowledge, archive, integrate

```console
$ onto gate add-search
[Guides] How is the guides obligation resolved?
    updated — The affected guides were written/updated
    waived:<reason> — Waived with a recorded reason
  → onto set guides add-search <value>
[Integration] How should the branch be integrated at close?
  * merge — Merge the branch into its base branch
    pr — Open a pull request and leave the branch for review
  → onto set integration add-search <value>
[Close plan] Has the close plan been validated against the verified workspace? merge-deltas and close refuse without this.
  → onto set close-confirmed add-search "<evidence>"
[Specs merged] Have the change's spec deltas been merged into the living specs?
  → onto merge-deltas add-search
```

```console
$ onto set guides add-search updated
$ onto set integration add-search merge
$ onto set close-confirmed add-search "deltas lint clean; ADR 0007 numbered; merge into main"
$ onto merge-deltas add-search
  merged search → docs/specs/search.md
add-search: 1 delta spec(s) merged; marked close.merged
$ git add -A && git commit -m "close add-search"
$ onto close add-search
add-search: archived to docs/changes/archive/2026-10-27-add-search; integration pending
```

ADR drafts get numbers and move to `docs/adr/`; affected guides are updated
(or the obligation is waived with a reason). Then the branch is integrated and
the receipt recorded:

```console
$ git switch main && git merge --no-ff onto/add-search
$ onto complete-integration add-search --receipt merge:$(git rev-parse HEAD)
add-search: integration complete for repository config (merge:b7e101e…)
```

Receipts: `merge:<sha>` (a real two-parent merge containing the verified
candidate), `pr:<url>` (an opened PR; an external assertion), or
`unchanged:<sha>` (a proven no-op). Only after every repository's receipt is
the change `done`.

## Presets: fix and tweak

```console
$ onto new fix-empty-query --workflow fix
$ onto set isolation fix-empty-query branch
$ onto advance fix-empty-query --to build
fix-empty-query: open → design
fix-empty-query: design → build
```

- **fix** — a bug: open-lite (`proposal.md` + `tasks.md`), then build
  **starting from a failing test that reproduces it**, verify, close.
- **tweak** — a small non-bug change: copy, config, docs, prompts, a small
  feature.

Both skip design and `plan.md`, are exempt from `proposal-approved` and
`approach-confirmed`, and still need `close-confirmed`. `--to build` walks
the gated hops in one call. When scope outgrows the preset (see
[eligibility](workflows.md#preset-eligibility)), the change upgrades:

```console
$ onto set workflow fix-empty-query full
```

…which returns it to design and invalidates build, verify, and close
evidence.

Changes **without delta specs** (presets, documentation-only changes) declare
each scenario once, on its own line, in either `tasks.md` or
`verification.md`:

```markdown
Scenario-ID: SC-fix-empty-query
```

## Who records what

| Token | Recorded by | Meaning |
|---|---|---|
| `proposal-approved` | `onto set proposal-approved <c> "<basis>"` | The proposal was reviewed against the request (full only). |
| `approach-confirmed` | `onto set approach-confirmed <c> "<basis>"` | An approach was chosen and why (full only). |
| `isolation` | `onto set isolation <c> branch\|worktree` | How build work is isolated. |
| `verify-result` | `onto set verify-result <c> pass\|fail` | Overall verification outcome; `pass` captures source HEADs. |
| `guides` | `onto set guides <c> updated\|waived:<reason>` | User docs kept current (full only). |
| `integration` | `onto set integration <c> merge\|pr` | How the branch lands. |
| `close-confirmed` | `onto set close-confirmed <c> "<basis>"` | The close plan was validated. |

Who answered and when comes from Git: the state file and artifacts are
committed, so `git log` over the change directory attributes each answer.

## Specialists in onto

| Agent | Used in | Concurrency |
|---|---|---|
| `onto-explorer` | open, design, triage | one per independent question |
| `onto-implementer` | build, repairs | one per checkout; parallel across repositories or worktrees |
| `onto-reviewer` | build (per task), verify (whole diff) | one per lens |
| `onto-skeptic` | verify | ≥2 per round |

## Working with a dirty tree

`onto dirt <change>` classifies every uncommitted path:

```console
$ onto dirt add-search
?? docs/changes/add-search/design.md (own)
?? docs/changes/add-search/tasks.md (own)
 M docs/changes/other-change/proposal.md (change)
 M src/cache.go (source)
3 uncommitted path(s), 2 blocking close
```

| Class | What | Blocks this change's close |
|---|---|---|
| `own` | this change's records | yes — commit them |
| `change` | another change's records or the archive | no |
| `source` | any other path in the selected checkout | yes, until resolved |

So two changes can be in flight at once without blocking each other. The
binary decides *what blocks*; the coordinator decides *whose* a source edit is
and asks you when unclear.

## Picking a change up cold

| Question | Answer |
|---|---|
| Where is it really? | `onto state <c> --json` — `derived_phase` comes from the files; `phase_mismatch` flags a stale claim |
| What's next? | `onto handoff <c>` — phase, pending gates, excerpts; the first unchecked task |
| What's blocking? | `onto gate <c>` |
| Was it decided or assumed? | evidence tokens and `notes.md` |
| Did it really pass? | `verification.md`, `onto trace <c>`, `.onto/evidence.json` |
| Is the workspace healthy? | `onto doctor` |

`onto handoff <c> --write` persists a metadata-only pack under
`.onto/handoff/` (no prose, so pasted secrets can't leak into it).

## Dependencies between changes

```console
$ onto set deps add-search --dep storage-v2
$ onto graph
$ onto graph --check     # non-zero on a cycle
```

Build entry requires dependencies complete and no cycle; close requires every
dependency integrated. `supersedes` and `deviates-from` record other
relationships, shown in `onto graph`.

## Emergency bypass

Only when you explicitly ask, via `/onto-bypass`:

```sh
onto bypass add-search --to close --reason "hotfix shipped manually; record catch-up"
```

It skips transition, evidence, dependency, merge, and worktree checks, and
appends an audit record to `.onto/bypass.json`. `--to archive` archives
without merging specs or ADRs; it is not a successful close.

## Multi-repository changes

`onto new add-search --repo api --repo web` selects source repositories. With
schema 2, each selected repository gets frozen base and target anchors,
optional registered worktrees, and its own integration receipt. See
[workspaces](workspaces.md).
