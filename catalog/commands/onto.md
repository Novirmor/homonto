---
description: Start or resume the onto spec-driven workflow for this repo.
agent: onto
---

# /onto

Drive the **onto** five-phase workflow (open → design → build → verify → close)
for this repository. If the `onto` skill is installed, load and follow it;
otherwise tell the user the onto framework is not installed and stop (install it
with `homonto apply` after declaring `[frameworks.onto]`).

The `onto` skill is the dispatcher — it does exactly four things, in order, and
never performs phase work itself:

1. **Preflight** — verify the `onto` binary is available (`onto version`); it is
   the single authority for `onto-state.yaml` and a hard dependency. Warn (never
   halt) on any declared tooling provider that is missing — the dispatcher's
   generated `references/tooling.md` says which are declared.
2. **Discover** — find the active change under `docs/changes/` (or, if there is
   none and `$ARGUMENTS` describes new work, start one with `onto new`).
3. **Derive** — cross-check the recorded phase against real file state; the state
   file is a cache of truth, not truth.
4. **Route** — load the matching sub-skill (`onto-open`, `onto-design`,
    `onto-build`, `onto-verify`, `onto-close`, or the `onto-fix` / `onto-tweak`
    presets) for the derived phase and continue through later phases unless the
    user names an endpoint or asks to pause.

Every state mutation goes through the `onto` binary (`onto new`, `onto set …`,
`onto advance`, `onto close`, `onto complete-integration`) — never hand-edit
`onto-state.yaml` or workflow sidecars.

`$ARGUMENTS`, if present, describes what to work on — use it to open a new change
or to focus the current phase. If absent, resume the active change.
