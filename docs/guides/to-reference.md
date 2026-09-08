# to reference — commands and behavior

The `to` binary's complete command surface. Concepts — the phases, the plan
contract, the skills and subagents, and onto/to complementarity — are in the
[to workflow guide](to-workflow.md); design rationale is in
[to-framework-design.md](../to-framework-design.md).

## The gate

The mutating commands (`init`, `new`, `phase`, `bypass`, `done`, `abandon`) refuse
until, in order:

1. `homonto.toml` exists at the workspace root,
2. it declares a `[frameworks.to]` table, and
3. `.homonto/catalog/skills/to` exists as a directory (the declaration has
   been applied).

Each failure names the fix (`homonto init`, declare `[frameworks.to]`, run
`homonto apply`). `promote` is the exception: it bridges the two frameworks,
so either an applied `[frameworks.to]` or an applied `[frameworks.onto]`
passes its gate. Read-only forms never write, but resolve `homonto.toml` for
the records root and selected source scope when needed. Legacy config-free
recovery uses `docs`; a broken configured root is not an empty workspace.

Change names are lowercase-alphanumeric segments joined by single hyphens
(`fix-login`, `update-deps`); `archive` is reserved.

## Commands

Workspace commands support `--dir <root>`. `init`, `new`, `status`, `phase`,
`done`, `abandon`, and `handoff` also support `--json`. `doctor` instead offers
`--quiet` for exit-code-only checks, while `version` prints plain text and does
not inspect a workspace.

`<workflow-root>` means `[workflow].root` in `homonto.toml` (default `docs`).

| Command | What it does |
|---|---|
| `to init` | Scaffold `<workflow-root>/tasks/` + `<workflow-root>/tasks/archive/` (gated; never overwrites). |
| `to new <name> [--repo <declared-name>] [--base <alias>=<branch>]` | Create a change at phase `plan` with an empty `plan.md` under `<workflow-root>` (gated). `--repo` selects source aliases; schema-2-only `--base` selects local integration branches. Both flags are repeatable. Active changes and retained worktree bindings block name reuse. |
| `to phase <name>` | The one forward transition: `plan → do` (gated). Finishing is `to done`; there is no other advance. |
| `to bypass <name> --to <plan|do|done|archive> --reason "<reason>"` | Emergency operator command, available through the command-only `/to-bypass` entry point after an explicit user request. Sets the target directly and skips phase, verification, and worktree gates, but still requires the framework, valid readable state, and a working archive filesystem. `done`/`archive` archive with `verified: false`; every use records the command, source/target, reason, timestamp, and skipped checks in `.to/bypass.json`. |
| `to done <name> --verified [--evidence "<text>"]` | Mark done and archive (gated). `--verified` is **required but self-asserted**; the binary does not run verification. Scoped changes require clean, determinable selected execution checkouts as described below. `--evidence` is recorded verbatim and unchecked. Requires phase `do`, except interrupted-archive recovery. |
| `to abandon <name>` | Terminal exit without done; archives (gated). Works from any non-terminal phase. |
| `to status [--all]` | Active changes and phases; corrupt state and source-scope errors are reported per entry. Root resolution, unsafe layout, and directory-read errors fail the command rather than appearing as an empty inventory. A genuinely absent tasks tree is empty. `--json` emits an array; `--all --json` emits `{tasks, onto}` for both workflows. Read-only. |
| `to handoff <name>` | Compact recovery pack: identity, phase, safe next skill, and a plan excerpt (head, complete unchecked task contracts, `Final Verify:`, and bounded notes/verification sections) for resuming after compaction. A missing `plan.md` is reported, not silently omitted. Read-only unless `--write` is selected. |
| `to doctor [--quiet]` | Workspace health: invalid state files, wedged terminal-but-active changes (an interrupted archive — re-run the finishing command to converge), missing `plan.md`, `do`-phase tasks missing non-empty `Files:`, `Change:`, or `Verify:` fields, a missing or empty `Final Verify:`, non-terminal archive entries, binary↔framework version skew, and unavailable selected repos for active scoped changes. These are diagnostics, not transition gates. `--quiet` prints nothing and signals via exit code only — the hook primitive. Read-only. |
| `to version` | The release-stamped version. |

### Source bases at creation

Schema 2 requires at least one selected `--repo` alias and has no implicit
config source. Creation freezes each source's current HEAD and attached local
branch unless you supply `--base <alias>=<branch>`. An override selects an
existing local branch's tip and integration target without switching the dirty
checkout or changing its index or files:

```bash
to new tune-cache --repo api --repo web --base api=main --base web=develop --dir /work/control
```

Each override must name a selected alias and may appear only once per alias.
Choose the target before record creation: subsequent worktree creation must
match both the frozen commit and branch, not rebase or retarget them. Schema
0/1 rejects `--base` and retains legacy source selection. Use the config root
for `--dir`, even from an execution worktree. See [workspaces](workspaces.md).

The current `to-state.yaml` schema is **1**, separate from config schema 2.
Its `repos`, `repo_mode`, and `repo_bases` fields preserve selected source
provenance; each base carries `git_common_dir` and explicit sources' required
`base_ref` / `base_branch`. Legacy provenance may omit those anchors. Conversion
carries this recorded scope and its existing anchors, not today's HEAD.

### Source clean gate

Explicit schema-2 changes audit only selected aliases, using registered
execution worktrees when present, not dirty original checkouts or a receiver.
Independent records Git and config Git are not additional sources. In an
`existing` combined layout, the configured records subtree (including its
counterpart in a bound execution checkout) is excluded from the source check.
Other source dirt still blocks, including a source deletion renamed into records.
Invalid source identities, aliases, or bindings fail closed.

Legacy scoped changes audit config Git plus selected siblings, excluding the
current task's own records and lock in the config checkout. Legacy unscoped
changes remain Git-blind. Changing today's schema does not convert their
recorded source scope.

## Archive naming

A change finishing on date D archives to `<workflow-root>/tasks/archive/<D>-<name>/`;
a same-day reuse of the name gets a numeric suffix (`<D>-<name>-2`).
Pre-v0.5.0 unprefixed archive directories are still recognized.

In managed mode, the exact archive destination/date and source-state fingerprint
are prepared before pre-write history intent. The handler uses that same plan,
including the stored finish date on recovery, and rechecks source identity and
target safety rather than recomputing dates or suffixes after gates. Midnight
cannot make the journal and actual archive disagree; replacement or collision
fails closed. See [records history](workspaces.md#records-history-and-source-commits).

A retained binding reserves the name in the same workflow after archive or
abandonment, even if the next change selects different repos. Remove or recover
the old binding before reuse; archiving alone does not free it.

## Crash safety

`done` and `abandon` write the terminal state, then move the directory into
the archive. If that is interrupted, the change is left terminal-but-active:
`to doctor` reports it, and **re-running the same finishing command completes
the archive** (`to done <name> --verified` / `to abandon <name>`), dating the
archive by the recorded finish. Commands that mutate a change (`new`,
`phase`, `done`, `abandon`) take a workspace lock (`<workflow-root>/tasks/.to.lock`), so
two concurrent sessions fail fast instead of interleaving writes. `init` only
creates the fixed directories idempotently and does not lock. A lock left by
a killed process names its pid; once that pid provably no longer runs, the
next mutating command reclaims the lock itself. A lock with no readable pid
(a crash in the create-to-write window) is removed by hand — a live
session's lock is never stolen.

Archive recovery preserves the stored `verified`, `evidence`, and finish date;
the new invocation's `--verified` does not rewrite old verification. JSON reports
the actual stored `verified` value, including `false` for an interrupted bypass,
not a fabricated `true` merely because recovery required the flag.
In managed mode, recover a pending history journal with
`homonto workspace recover` first, then inspect state before deciding whether
the finishing command still needs to run.

## Recovery packs — `to handoff <name> [--json] [--write]`

`handoff` prints the resume pack. `--json` keeps the legacy keys
(`change`/`state`/`plan`/`next`) and adds the versioned recovery-envelope
fields (schema, operation id, derived phase, repo aliases, artifact
digests, next argv). `--write` persists the metadata-only recovery view
under `<workflow-root>/tasks/<name>/.to/handoff/` — never plan prose or evidence text.

## Promotion — `to promote <name> [--as <name>] --yes`

Converts a growing `to` change into a full onto change (ADR 0028): the
complete source workspace moves unchanged into the neutral control plane
(`<workflow-root>/changes/<name>/.workflow/snapshots/`), and a fresh
proposal-only workspace starts at phase open — promotion claims no design or
verification. The command prints the next steps: declare
`[frameworks.onto]` in `homonto.toml` (alongside `to`, if not already),
`homonto apply --yes`, then `/onto`. The frameworks are complementary, so
nothing must be removed; a crash between the moves is recovered
idempotently (receipt-verified) and tampered staging is refused. `onto
demote <name> --yes` mirrors the conversion back (ADR 0042) — an unchanged
immediate inverse restores the original workspace byte-for-byte.

After edits, demotion from onto build/verify continues at `do` only with complete
matching task contracts: `Owner:`, `Repo:`, absolute `Cwd:`, `Files:`, `Do:`
(rendered as `Change:`), and `Verify:`. Execution scope and multiline fields,
including verification commands, survive translation. `Final Verify:` keeps
its full recorded value, or uses the last task's check if no aggregate exists.
`DEFERRED TO CLOSE:` tasks transfer unchecked rather than pretending their
deferred work is done. Incomplete, placeholder, or ambiguous contracts restart
at `plan` with the source snapshot retained, not invented execution detail.

Registered worktree bindings block conversion. There is no ownership-safe
active-binding conversion or rebind command; continue in the current workflow,
not by deleting registry entries or trying to remove an active binding.

## What `to` deliberately does not do

No evidence gates (the `--verified` checkbox is an assertion, not a
guarantee — the `to-done` skill is where verification rigor lives), no spec
deltas, no dependency graph, and no automatic source integration. Managed mode
does checkpoint operation-scoped records, and schema 2 supports one registered
execution worktree per change/repo, but not per-implementer orchestration.
Escalation is an explicit conversion
(`to promote`), never a phase. A
scoped change has only the terminal clean-worktree gate; unscoped changes stay
Git-blind. If a change needs the heavier workflow, the repo needs onto.

`to` does run subagents concurrently — the three read-only ones, per
[ADR 0035](../adr/0035-deny-shell-access-to-concurrent-specialists.md). What it does not
do is run two *writers*: `to-implementer` remains one at a time. A receiver is
a separate integration checkout, not another implementation slot.
