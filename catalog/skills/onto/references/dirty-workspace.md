# Dirty-workspace protocol

Follow the shared [workspace and dirty-work policy](../../homonto/references/workspace-policy.md).
It applies before writes, on resume, and in every phase, not just build/close.
Present exact paths and honor the preserve/isolate/cleanup choice; ask once when
none exists. Read-only research proceeds. Preserve never waives a clean gate.

Run `onto dirt <change> --json --dir "<configRoot>"` for the binary's structural
classification and blocking paths. `own` records belong to this change, `change`
records to another change, and `source` paths require attribution. Inspect staged
and unstaged diffs and untracked paths in the reported roots; classification is
not ownership or permission to commit, stash, reset, or delete user work.

The gate resolves selected source aliases through registered execution bindings.
Do not run from an unregistered clean checkout to evade dirt in the gated root.
Checkpoint managed Markdown records separately from source commits; in existing
mode commit only the named records this operation owns. Never mark verification
passed on unexplained source dirt, check off a task from dirt alone, or launder
unrelated work into a final commit to make close pass.
