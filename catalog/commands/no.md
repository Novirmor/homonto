---
description: Run the minimal no development and review engine.
---

# /no

Load and follow the `no` skill. If it is unavailable, say that the `no` engine
is not installed and stop. `$ARGUMENTS` describes the work to perform.

This task selects `no` exclusively, even when other engines are installed. Keep
that choice on resume. Never invoke onto/to or create their lifecycle records.
Read the no skill's generated `references/workspace.md` when present for declared roots.

For multirepository work, ask once whether to use existing checkouts or worktrees,
with per-repo exceptions; preserve the answer on resume. The parent may use
`homonto workspace task create NAME --engine no --repo ALIAS --isolation MODE`
(repeat `--repo`), then `homonto workspace task inspect NAME`. Pass the actual
`--config` path. These commands require schema 2 and declared repos; shared
`.homonto/workspace-tasks` bookkeeping is not an engine lifecycle or plan.
Ask for disposition of dirty verification inputs; an explicitly approved clean
snapshot uses `--isolation-override ALIAS=worktree` and preserves original edits.

The parent chooses dependencies and concurrency: one repository per implementer
assignment and one writer per checkout. Supply each worker the selected engine,
writable alias and absolute checkout, base, task/verification contract, and all
declared repo paths for reading. Writes outside the assigned repo are forbidden
by the assignment, not a runtime sandbox. Continue independent work around
blockers; pause dependent work. No scheduler or cross-session reservation exists.

Implementers return edits and evidence. The parent owns commits, integration,
and publication. Review completed repo candidates in parallel with no writers
in those checkouts, then review combined interfaces and integration evidence.
Commit candidates, pause participating writers, and run
`homonto workspace task check NAME --writers-paused`. Named configured checks
run when any listed repo changes, using pinned clean commits for unchanged
inputs and the generated checkout-aware JSON context. Later changes invalidate
results. Choose fallback verification only if no configured check matches;
never substitute for a configured failure. Ask before partial delivery.
After authorized integration, `homonto workspace task cleanup NAME --yes` removes
only clean integrated task-owned worktrees and retains their branches.
