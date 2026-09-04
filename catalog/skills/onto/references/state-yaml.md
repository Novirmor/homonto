# onto-state.yaml — canonical schema and template

The binary-owned workflow state for one change. This file is the canonical
source (`<workflow-root>/changes/README.md`, when the repo has one, points here rather
than restating it). **`onto-state.yaml` is written exclusively by the `onto`
binary** (`onto new`, `onto set …`, `onto advance`, `onto close`, `onto
complete-integration`, `onto abandon`) — never hand-edit it. The dispatcher re-derives the *routing* phase
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
schema_version: 2
change: <name>             # must equal directory name
id: <stable-id>            # assigned once at `onto new`, never rewritten
workflow: full             # full | fix | tweak
phase: open                # open | design | build | verify | close
created: YYYY-MM-DD
base_ref: <canonical commit sha; resolves in the repo, set-once>
base_branch: <current branch, captured separately for close integration>
deps: []                   # change names that must archive before this builds
repos: []                  # selected declared [repos] aliases; config repo is implicit
supersedes: []             # change names this change replaces (ungated, traceability)
deviates_from: []          # targets this change knowingly diverges from (ungated)
isolation: null            # branch | worktree (required before entering build)
integration: null          # merge | pr (recorded at close; acted on by onto-close skill)
integration_required: false # set true at close; a missing .onto/integration.json then fails closed
build_mode: null           # direct | subagent
build_pause: null          # plan-ready | (cleared) — set only for an explicitly requested pause
tdd_mode: null             # tdd | direct
verify:
  scale: null              # light | full (set at verify entry by scale rules)
  result: pending          # pending | pass | fail
  heads: {}                # alias → HEAD frozen at a recorded pass; close enforces it
close:
  merged: false            # set true by `onto merge-deltas` after spec deltas land
directive: null            # verbatim user pre-authorization text, if any
proposal_approved: null    # open's recorded proposal review; refuses `advance` out of open (full only)
approach_confirmed: null   # design's recorded approach selection; refuses design → build (full only)
close_confirmed: null      # recorded close-plan validation; refuses `merge-deltas` and `close` (all workflows)
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

- `phase` advances only when a phase's evidence is recorded via `onto advance`
  — never because artifacts happen to exist (recorded decisions win upward).
- `directive` holds explicit user instructions verbatim. Ordinary continuation
  needs no special directive.
- `isolation` is required before entering build (the binary refuses the
  design→build advance without it). Choose it at the design exit gate (see
  `onto-design`).
- The three **evidence tokens** hold a truthful review summary and are set only
  by `onto set proposal-approved|approach-confirmed|close-confirmed <change>
  <evidence>`. The binary refuses the transition while the token is empty, so a
  review cannot disappear between sessions. Git records who wrote each token.
  `onto gate <change> --json` lists pending decisions with the exact setter. The two full-only
  tokens are not required of `fix`/`tweak` presets; `close_confirmed` is
  required of every workflow.
- `deps` names other changes under `<workflow-root>/changes/`; a legacy archive resolves a
  dependency, while a tracked archive resolves it only after
  `.onto/integration.json` records completion. Archive matching uses the
  date-anchored exact name, never a bare suffix. The dispatcher warns before
  resuming a change whose deps are not all complete; a dep matching no active or archived change, a self-dep,
  or a dep cycle reaching this change are findings to correct or drop.
- `repos` is set only by `onto new --repo <declared-name>` and names sibling
  repositories from `homonto.toml`; it never stores paths. The config repo is
  implicit. Before close, `onto` audits that repo plus every selected sibling;
  any dirty or unavailable selected worktree fails the close gate.
- `base_ref` is the immutable commit used for diffs and verification. The
  setter resolves and canonicalizes it — an unresolvable ref is refused.
  `base_branch` is the branch used for local merge or as a pull request base.
  Never pass the commit-valued `base_ref` where Git or GitHub expects a branch.
- A preset upgrades through `onto set workflow <name> full`; this is the only
  supported workflow change. It returns the change to design and invalidates
  old build, verification, guide, merge, and close evidence.
- `close.merged` is set by `onto merge-deltas` after the current spec deltas
  merge and lint clean. `.onto/merge-receipt.json` binds it to the exact delta
  manifest plus living-spec pre/post-images; the compatibility setter delegates
  to the same merge operation and cannot forge the flag.
- `verify.heads` is frozen when `onto set verify-result <change> pass` records
  a pass (alias `""` is the config repository). Close refuses when a scoped
  repository has commits past its head that are not workflow bookkeeping
  (<workflow-root>/ paths in the config repo; nothing at all in a selected sibling), or
  when a head no longer resolves. Recording a new pass re-binds; any other
  result clears the heads along with the evidence they justified.
- `integration` must be recorded before close. The skill derives `merge` or
  `pr` from repository policy while the workspace is still active.
- `onto close` writes `.onto/integration.json` as pending before archival and
  stamps `integration_required: true` into the archived state. The record
  carries one entry per repository in the change's scope (config plus every
  selected sibling), each freezing its base branch tip, source branch, and
  source commit. `onto complete-integration [--repo <alias>] --receipt …`
  completes one repository at a time; a merge receipt must name a real
  `--no-ff` merge commit that integrated the recorded source into the recorded
  base branch (the binary verifies parents and reachability against git), and
  a PR receipt is the opened PR's https URL. The change derives `done` and
  resolves dependencies only when every repository is complete. A required
  archive whose sidecar is missing or invalid fails closed (derives `close`);
  archives from before `integration_required` are legacy and remain terminal.
- Reused change names: the newest dated archive generation is authoritative
  for `state`, `close` recovery, and `complete-integration`; dependency
  resolution never falls back from a pending or malformed newest generation to
  an older completed one. `onto new` refuses a name whose latest archive has
  not completed integration.
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
3. Restore a tracked state file from Git when possible. If it is genuinely lost,
   stop for explicit destructive-recovery intent and quarantine the orphaned
   workspace before creating a fresh change. `onto abandon` cannot load a
   missing state file and is not a recovery command.

Cap the resumed phase per the boundary table below so a lost state file does
not skip reviews or decisions that have no durable evidence.

## Gate caps for phase recovery (boundary → record consulted)

| Boundary | Gate | Decidable from |
|---|---|---|
| open → design | artifact review | notes.md Confirmed entry (onto-open exit mandates recording it) |
| design → build | approach selection + isolation | notes.md Confirmed entry (onto-design records it); `isolation` must be re-chosen |
| build → verify | build configuration and completed plan | notes.md Confirmed entry plus checked tasks and passing task evidence |
| verify → close | none (failure gate fires only on fail) | `verification.md` with `Result: pass` — no demotion when present |

Rules: demote **one boundary at a time** until the boundary's record is found
(or the floor). The floor is `open` for full workflow and **`build` for
presets** (presets have no open/design phases; their open-lite classification
and technical defaults are re-derived at build entry). With notes.md missing
entirely, demotion iterates through every notes-dependent boundary down to the
floor — only verify→close resists, staying decidable from verification.md
regardless.

**Upgraded presets** (a `Preset: … (upgraded to full …)` marker) never floor
at `open`: the automatic upgrade annotation is itself the open→design record,
and a `design.md` marked `Status: Confirmed` is itself
the design→build record. Their floor is `design` without a confirmed design.md,
`build` with one. A recovery must not send a change with a recorded
design back to clarification.
