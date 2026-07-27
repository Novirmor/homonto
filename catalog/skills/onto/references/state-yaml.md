# onto-state.yaml — canonical schema and template

The binary-owned workflow state for one change. This file is the canonical
source (`docs/changes/README.md`, when the repo has one, points here rather
than restating it). **`onto-state.yaml` is written exclusively by the `onto`
binary** (`onto new`, `onto set …`, `onto advance`, `onto close`, `onto
abandon`) — never hand-edit it. The dispatcher re-derives the *routing* phase
from file state on every run; the binary's `phase` field is the authoritative
record of where the workflow stands, and files win only for routing decisions
(see the dispatcher's §3).

A legacy `state.yaml` (the pre-binary, agent-managed shape with nested
`decisions:`, `verify.mode:`, `metrics:`) is migration input only:
`LoadChange` reads it when no `onto-state.yaml` exists, or merges its
observational data when both are present. The current binary never writes it.
If you encounter a change with only a legacy `state.yaml`, treat it as a
recovery situation (see the dispatcher's §3.5).

## Template (the shape the binary writes)

```yaml
schema_version: 1
change: <name>             # must equal directory name
id: <stable-id>            # assigned once at `onto new`, never rewritten
workflow: full             # full | fix | tweak
phase: open                # open | design | build | verify | close
created: YYYY-MM-DD
base_ref: <git rev-parse HEAD, captured when open creates the workspace>
deps: []                   # change names that must archive before this builds
supersedes: []             # change names this change replaces (ungated, traceability)
deviates_from: []          # targets this change knowingly diverges from (ungated)
isolation: null            # branch | worktree (required before entering build)
integration: null          # merge | pr (recorded at close; acted on by onto-close skill)
build_mode: null           # direct | subagent
build_pause: null          # plan-ready | (cleared) — a deliberate pause at the plan-ready gate
tdd_mode: null             # tdd | direct
verify:
  scale: null              # light | full (set at verify entry by scale rules)
  result: pending          # pending | pass | fail
close:
  merged: false            # set true by `onto merge-deltas` after spec deltas land
directive: null            # verbatim user pre-authorization text, if any
proposal_approved: null    # open's artifact-review answer, verbatim; refuses `advance` out of open (full only)
approach_confirmed: null   # design's approach answer, verbatim; refuses design → build (full only)
close_confirmed: null      # close's final-confirmation answer, verbatim; refuses `merge-deltas` and `close` (all workflows)
guides: pending            # pending | updated | "waived: <reason>" (quoted — bare waived: is invalid YAML)
archived: false            # set true at archive; phase stays "close"
                           # ("done" is derived-only, never written)
abandoned: false           # set true by `onto abandon` (the unsuccessful terminal)
observed:                  # observational only — never a gate, never blocking
  metrics: {}              # <phase>: YYYY-MM-DD stamped at each phase exit
  tasks_total: 0           # finalized at close (checked tasks)
  verify_rounds: 0         # incremented per recorded verify fail
  preset_escalated: false  # a preset→full upgrade happened (legacy carry-over)
```

## Field rules

- `phase` advances only when a phase's exit gate is answered via `onto advance`
  — never because artifacts happen to exist (gates win upward).
- `directive` holds blanket pre-authorizations verbatim; it pre-answers only
  the gates whose skill text says MAY be pre-authorized.
- `isolation` is required before entering build (the binary refuses the
  design→build advance without it). Choose it at the design exit gate (see
  `onto-design`).
- The three **evidence tokens** hold the user's answer verbatim and are set
  only by `onto set proposal-approved|approach-confirmed|close-confirmed
  <change> <evidence>`. They are what makes a user gate non-skippable: the
  binary refuses the transition while the token is empty, so an honest agent
  cannot advance past a question it never asked. `onto gate <change> --json`
  lists whichever are still pending with the exact setter. The two full-only
  tokens are not required of `fix`/`tweak` presets; `close_confirmed` is
  required of every workflow.
- `deps` names other changes under `docs/changes/`; a dep is archived iff
  `docs/changes/archive/????-??-??-<dep>/` exists (date-anchored exact name,
  never bare suffix). The dispatcher warns before resuming a change whose deps
  are not all archived; a dep matching no active or archived change, a self-dep,
  or a dep cycle reaching this change are findings to correct or drop.
- `close.merged` is set exclusively by `onto merge-deltas` after the spec deltas
  merge and lint clean. Do not set it by hand — `onto set close-merged` exists
  for recovery but bypasses the actual merge.
- `observed` is best-effort observational data. Never block on it for any
  reason.

## Recovery (lost / malformed onto-state.yaml)

A missing or malformed `onto-state.yaml` is a recovery situation, never a
silent rewrite. **Do not hand-write a replacement** — the binary is its sole
authority. The honest path:

1. Re-derive the *routing* phase from the file-evidence table in the
   dispatcher's §3 (this decides where work resumes, not what the binary
   records).
2. Surface the recovery to the user and record it in `notes.md`.
3. If the file is genuinely lost, `onto abandon <name>` the orphaned workspace
   and `onto new` a fresh one, then resume at the derived phase. The fresh
   `onto-state.yaml` carries the current schema; the recovered routing decides
   where work picks up.

Cap the resumed phase per the boundary table below so a lost state file does
not skip what the user never confirmed.

## Gate caps for phase recovery (boundary → record consulted)

| Boundary | Gate | Decidable from |
|---|---|---|
| open → design | artifact review | notes.md Confirmed entry (onto-open exit mandates recording it) |
| design → build | approach confirmation + isolation | notes.md Confirmed entry (onto-design records it); `isolation` must be re-chosen |
| build → verify | plan-ready | notes.md Confirmed entry (onto-build's gate records its answer there) |
| verify → close | none (failure gate fires only on fail) | `verification.md` with `Result: pass` — no demotion when present |

Rules: demote **one boundary at a time** until the boundary's record is found
(or the floor). The floor is `open` for full workflow and **`build` for
presets** (presets have no open/design phases; their open-lite confirmations
are re-defaulted, not re-asked, at build entry). With notes.md missing
entirely, demotion iterates through every notes-dependent boundary down to the
floor — only verify→close resists, staying decidable from verification.md
regardless.

**Upgraded presets** (a `Preset: … (upgraded to full …)` marker) never floor
at `open`: the upgrade annotation is itself the open→design record (the user
confirmed the upgrade), and a `design.md` marked `Status: Confirmed` is itself
the design→build record. Their floor is `design` without a confirmed design.md,
`build` with one. A recovery must not send a change with a user-confirmed
design back to clarification.
