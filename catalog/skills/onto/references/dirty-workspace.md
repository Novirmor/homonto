# Dirty-workspace protocol

Canonical path: `onto/references/dirty-workspace.md`. Loaded whenever a
session starts, resumes, or is dispatched into a workspace whose git tree has
uncommitted paths. The split of labor is fixed: **the binary owns
what-is-dirty and what-blocks-close; you own attribution** — deciding whose
work an uncommitted source path is and what to do with it.

## 1. Measure, don't guess

```bash
onto dirt <change> --json    # every uncommitted path, classified
```

Classes (structural, not judgment):

| Class | Meaning | Blocks this change's close? |
|---|---|---|
| `own` | under this change's `<workflow-root>/changes/<name>/` (or an untracked ancestor dir containing it) | **yes** — its evidence must be committed |
| `change` | under another change's `<workflow-root>/changes/<other>/` or the archive | no — that change's own close gate owns it |
| `source` | anything else in the repo | **yes** — until attributed and committed, stashed, or discarded with authorization |

Inspect content with `git diff`, `git diff --cached`, and by reading new
files. Omit the change argument (`onto dirt`) when no change is in scope.

## 2. Attribute `source` dirt

Uncommitted source paths may be the user's, an interrupted task's, or a
stranger's. Classify each into exactly one:

1. **Belongs to the current change** — content matches its goal, `tasks.md`,
   plan, or delta specs. Fold it into the owning task explicitly: verify it,
   say so in that task's commit, never redo it and never build on top of it
   unknowingly.
2. **Does not belong** — leave it alone and use a worktree when isolation is
   needed. Do not ask merely to choose that safe default. Including or splitting
   it requires a matching user request; discarding always requires explicit
   authorization. Never revert, overwrite, stash, or reformat unattributed work.
3. **Unclear** — inspect the content and history first. If ownership still
   cannot be established and the dirt blocks safe progress, report the paths and
   ask who owns them; do not advance over unattributed dirt.

`change`-class dirt needs no attribution — leave it for its own change.

## 3. Phase rules

- Dirt is **code evidence only**: it never checks a task off, never advances
  a phase, and never substitutes for verification.
- Code dirt found during **open/design** is requirements or design input —
  record it in the proposal/design; it is not "implementation already done".
- Entering **close**: the gate refuses on any `own` or `source` path and its
  error lists the offenders. Commit what belongs, resolve what doesn't, retry.
  Do not launder unrelated work into the change's final commit to make the
  gate pass.
- Never mark verify passed while `source` dirt remains unexplained.
