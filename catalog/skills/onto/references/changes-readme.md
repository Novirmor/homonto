# docs/changes/README.md — canonical template

Bootstrap writes this into a repo at `docs/changes/README.md` when it
creates the onto layout. It is a **pointer**, not a second copy of the
rules — the onto skills stay the single source, so the two never drift.

## Template

```markdown
# Changes

Active and archived onto change workspaces.

- **Active change**: a directory directly under `docs/changes/` holding a
  `proposal.md` or `onto-state.yaml`, with `onto-state.yaml` reading
  `archived: false`. A directory with neither artifact is not a change.
- **Archived change**: under `docs/changes/archive/YYYY-MM-DD-<name>/`,
  `onto-state.yaml` `archived: true`. Archives are history — never edited, with
  the single exception of `ship.md`.
- **Abandoned change**: remains directly under `docs/changes/<name>/` with
  `abandoned: true`. Discovery excludes it, but it is not a successful archive.

## State model

`onto-state.yaml` is the binary-owned per-change workflow state. Its canonical
schema, field rules, and the phase-derivation table are defined by the onto
skill set (the `onto` dispatcher skill and its
`references/state-yaml.md`) — this README does not restate them, so there is
nothing to keep in sync. The dispatcher re-derives the real phase from file
state on every run; `onto-state.yaml` is the binary's record, and files win
for routing. Never hand-edit `onto-state.yaml` — every mutation goes through
the `onto` binary (`onto new`, `onto set …`, `onto advance`, `onto close`,
`onto complete-integration`, `onto abandon`).

## Layout contract

- `docs/changes/<name>/` — one active change: `onto-state.yaml`, `notes.md`,
  `proposal.md`, `tasks.md`, and (full workflow) `design.md`, `plan.md`,
  `adr/`, `specs/`, `verification.md` as the phases produce them.
- `docs/changes/archive/YYYY-MM-DD-<name>/` — a successfully closed change.
```
