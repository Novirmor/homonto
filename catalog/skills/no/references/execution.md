# No task execution

This reference owns the parent coordination contract for both `/no` and callers
such as h resolve. The no skill remains an informational inventory of its two
workers; no lifecycle plan, phase or binary is required.

Use OpenCode's built-in `question` tool for every question to the user, including
clarifications, free-form input and confirmations. Always display the native dialog
box, including on standalone entry. If unavailable, denied or dismissed, preserve
state and report the pending input; do not fall back to chat or shell prompts or
infer consent. Workers return questions to the parent, never prompt the user.

This task selects `no` exclusively, even when other engines are installed. Keep
that choice on resume. Never invoke onto/to or create their lifecycle records.
Read the no skill's generated `references/workspace.md` when present for declared
roots; otherwise inspect the active configuration. Inspect affected checkouts
before writes and preserve unrelated work. Ask once about exact dirty paths when
no existing disposition covers them. Never initialize Git, stash, reset, copy
private inputs or commit user edits as automatic setup.

For a new task, ask once whether to use existing checkouts or worktrees unless
the request already selects them, including per-repo exceptions; preserve the
answer on resume. In schema 2 with declared
repos, the parent creates or resumes the shared task with
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
For a recorded workspace task, commit candidates, pause participating writers,
and run
`homonto workspace task check NAME --writers-paused`. Named configured checks
run when any listed repo changes, using pinned clean commits for unchanged
inputs and the generated checkout-aware JSON context. Later changes invalidate
results. Choose fallback verification only if no configured check matches;
never substitute for a configured failure. Ask before partial delivery.
At delivery, run the recorded task's
`homonto workspace task cleanup NAME --yes` without asking. It removes clean
task-owned worktrees; an integrated branch is released, an unmerged PR
delivery keeps its branch.

For legacy configuration without workspace-task support, retain the confirmed
checkout choice and evidence in the session/handoff; do not invoke unsupported
task commands or create onto/to records. If requested isolation cannot be safely
established, report the concrete blocker rather than silently changing the choice.

Finish at the caller's requested endpoint. Keep candidate/base/tree OIDs,
verification outputs, integration targets and publication identities in session
evidence and the handoff. On resume inspect existing task metadata when present,
reconcile candidate reachability and remote delivery, and perform the first
missing action; missing evidence requires fresh verification, not a fabricated
record or a duplicate task. Publication still needs the caller's authority.
