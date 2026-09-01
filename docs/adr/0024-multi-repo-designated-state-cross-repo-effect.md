# ADR 0024: Multi-repo support — designated state, cross-repo effect

- **Status:** Proposed
- **Date:** 2026-09-01

## Context

homonto, onto, and `to` each anchor their state to the directory holding
`homonto.toml`: the projector writes `.homonto/` under the config repo, and
the workflow tools write `docs/changes/` / `docs/tasks/` there too. A
maintainer working across several related repositories must either replicate
a config per repo or run homonto once per repo, and an onto/to change that
spans repos has no home at all — each repo's workflow tree is isolated.

The v0.13.0 goal, set by the maintainer: **all homonto/onto/to files live in
designated places (one selected config repo), but operations can modify
other declared repos too.**

The safety contracts this moves:

- **Prune roots** (`copyfile` F7, `fileproj` managed roots): every path
  homonto deletes must resolve inside a root derived from the single config
  repo's layout. Cross-repo effect means roots per declared repo, and a rule
  for which declaration authorizes which repo.
- **Apply lock** (`applylock`): one project-scoped lock guards one repo's
  apply. Cross-repo apply needs the lock to cover every repo a plan touches,
  or two applies can interleave across repos even when each holds its own.
- **Drift semantics**: `homonto status` distinguishes drift vs. pending per
  repo; a multi-repo status must attribute findings per repo, not merge them.
- **onto/to workspaces**: `docs/changes/` and `docs/tasks/` are per-repo by
  design (onto's dirty-worktree checks are git-aware per repo). A
  designated-place workflow tree that reaches into other repos changes what
  `onto dirt` audits and what `to`'s workspace lock guards.

## Decision

We will build multi-repo in two stages, and only the first is committed for
v0.13.0:

1. **Declared repos (design, this release).** A `[repos]` table in the
   config repo's `homonto.toml` names the other repositories by path:
   `name = "../service-a"`. Paths resolve relative to the config file and
   must be git worktrees. All state stays where it is today —
   `.homonto/`, the apply lock, the remote cache, onto's `docs/changes/`,
   `to`'s `docs/tasks/` — in the **config repo only**. This is the
   designated-places invariant: one state home, many effect targets.
2. **Cross-repo projection (later).** Project-scoped resources gain
   `repo = "<name>"`; the engine builds one adapter per (tool, repo),
   applying the same surgical-merge contract inside each repo's
   `.opencode/`. Each repo gets its own managed roots and its own
   state.json partition (`state.<repo>.json`) so pruning and adoption stay
   scoped; the apply lock is held once, in the config repo, for the whole
   multi-repo plan. onto/to cross-repo changes (one change whose tasks edit
   several repos) follow after projection lands, because their gates audit
   git state per repo.

**Non-goals:** auto-discovery of repos under a root (declared, explicit,
auditable — a plan names every repo it will touch); moving state out of the
config repo into a user-global location; and any form of cross-repo write
that is not first visible in `homonto plan` output labeled per repo.

## Consequences

- Stage 1 alone changes nothing at runtime — `[repos]` parses, validates
  (paths exist, are git worktrees, no duplicates), and appears in plan
  output as context. It is the schema beachhead: later stages add meaning
  without a second breaking config change.
- Cross-repo effect (stage 2) will be a breaking-capability release of its
  own, gated on: per-repo managed roots with F7-style guards, one lock
  covering the whole plan, per-repo drift attribution in status/doctor, and
  Docker E2E suites that apply across two repos and assert isolation
  (an undeclared repo is never touched).
- The single-repo flow remains the default and first-class: no `[repos]`
  table, no behavior change.
