# Make tasks.md onto's single checkoff; plan.md carries detail only

- **Status:** Accepted
- **Date:** 2026-07-26

## Context

A full onto change maintained two task lists. `tasks.md` held numbered items
with checkboxes; `plan.md` held a task per item with its own `- [ ] done` line.
The skills required both to be updated together — "check BOTH files", discovered
work "mirrored into `tasks.md` in the same edit", and a subagent coordinator
duty to confirm "the checkoffs landed in both files".

Only one of them was ever read. `TasksAllChecked` parses `tasks.md` for the
build exit gate (`internal/ontocli/advance.go`) and for phase derivation
(`internal/ontostate/derive.go`). `plan.md` is checked for existence only
(`internal/ontostate/state.go`); nothing in onto parses its checkboxes, and the
close lint never mentioned the file at all.

So the second list was mandated, unenforced, and unread — hand-synced state
that could drift with nothing to detect it. The fix and tweak presets already
run with `tasks.md` and no `plan.md`, and the sibling `to` framework keeps one
task file and genuinely parses it (`internal/tocli/plan.go`).

## Decision

We will keep both files and give them one job each.

- `tasks.md` is the single source of completion state. Its checkboxes are what
  the gates and derivation read, and the only ones that exist.
- `plan.md` carries executable detail — Files, Do, Verify — and no checkboxes.
- The two are bound by number: `- [ ] 2.3` in `tasks.md` pairs with
  `## Task 2.3` in `plan.md`. Items and tasks are appended and superseded
  together, in the same edit.
- The close lint checks the correspondence in both directions, and flags any
  checkbox that appears in `plan.md`.

We rejected folding `plan.md` into `tasks.md` entirely. It would leave one file
and need no coupling rule, but it changes the required-artifact contract in the
binary and would bloat items that are deliberately one-line outcomes.

## Consequences

- One place to check a task off, so the two files can no longer disagree about
  what is done.
- They can still disagree about which tasks *exist* — an item appended to one
  file and not the other. The number pairing makes that visible and the close
  lint looks for it, but the check is prose an agent runs, not a gate the
  binary enforces. Drift is caught late, at close, or not at all.
- Reading a task now means reading two files. That was already true; it is
  simply no longer optional.
- No Go change, so no test caught this and none proves the new rule. The
  binary's behavior is unchanged: it still requires `plan.md` to exist and
  still reads only `tasks.md`.
- Existing in-flight changes with `- [ ] done` lines in `plan.md` are not
  migrated. The lint will flag them at close; the fix is deleting the line.
