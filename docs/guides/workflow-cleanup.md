# Clean up terminal workflows

The binaries own cleanup eligibility and safety checks. No agent is needed.
Cleanup does not finish or abandon active work, infer completion from age, or
delete archives.

## Inventory and preview

```bash
homonto workflow cleanup list --config /work/control/homonto.toml --json
onto cleanup list --dir /work/control --json
to cleanup list --dir /work/control --json
onto cleanup preview old-change --dir /work/control --json
```

These commands are read-only. JSON has `schema_version: 1`. Inventory contains
`config_path`, `workflow_root`, `items` and diagnostic `findings`. Each item names
its workflow, change, generation `identity`, native `lifecycle`, record path,
proposed archive destination, assessed worktrees, blockers and cleanup `status`:

| Status | Meaning |
|---|---|
| `ineligible` | Native work is active or integration is incomplete. Resume it, or explicitly abandon it using its engine. |
| `blocked` | A safety check or metadata validation failed. Inspect the reported blocker; confirmation cannot bypass it. |
| `cleanup-pending` | Terminal resources remain, or an interrupted cleanup receipt needs completion. |
| `cleanup-complete` | No registered worktree or active terminal record remains for this generation. |

Several generations can share a name. Select `--identity` from the inventory;
preview refuses an ambiguous name rather than choosing the newest record.

## Apply and retry

```bash
onto cleanup apply old-change --identity <identity> --yes --dir /work/control --json
to cleanup apply old-change --identity <identity> --yes --dir /work/control --json
homonto workflow cleanup apply old-change --workflow onto --identity <identity> --yes --config /work/control/homonto.toml --json
```

Apply rechecks the live generation and every selected binding. `--yes` is not a
force option. Dirty or ignored worktree content, hidden index flags, unintegrated
commits, changed ownership and ambiguous generations prevent removal. Active
records must also be clean before moving to an unused dated archive directory.
Native state bytes are preserved; an abandoned workflow stays abandoned.

Versioned receipts live under `<workflow-root>/.workflow/cleanup/`, outside the
archives. After interruption, inspect the same identity and repeat apply. A
recorded `removing` worktree is reconciled only when Git confirms removal; a
replacement checkout is never adopted. If managed history reports a pending
checkpoint, inspect and use `homonto workspace recover` before retrying cleanup.

Managed history checkpoints the receipt and selected record move. Existing Git
history leaves those edits for your normal commit. Native branches are retained;
cleanup does not prove remote delivery or authorize branch deletion.

For `no` task-created checkouts, continue using
`homonto workspace task cleanup <task> --yes --config <config>`. That command
retains task metadata and removes only its owned integrated allocations and
integrated task branches; it does not create an engine lifecycle.
