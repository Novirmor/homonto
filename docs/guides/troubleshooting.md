# Troubleshooting

Symptoms, causes, and fixes, grouped by area. Start with:

```sh
homonto doctor        # environment and projection health
homonto status        # drift vs. pending
onto doctor           # onto workspace health
to doctor             # to workspace health
```

## Installing

**`homonto: command not found` after the installer.** The installer never
edits your shell profile; it prints an `export PATH=…` line. Run it, add it
to `~/.bashrc` / `~/.zshrc`, and open a new shell.

**mise installed `homonto` but `onto`/`to` print the wrong version or are
missing.** Your mise is too old to honor `matching_regex` and installed the
`homonto` archive under every alias. Run `mise self-update` (2026.10.19 is
verified), then `mise install`. Check with `mise exec -- onto version`.

**Checksum mismatch.** The download didn't match `SHA256SUMS`, so nothing was
installed. Retry; compare against the release page's `SHA256SUMS` if it
persists.

**`go build .` fails: output "homonto" already exists and is a directory.**
A local `homonto/` content directory sits next to `main.go`. Build to a path:
`go build -o ./bin/homonto .`, or use `go install .` / `task build`.

**Version prints a development string.** Plain `go install` doesn't stamp
versions. Use a release, mise, or `task install`.

**New binary, old skills.** Installing a binary doesn't touch projected
content. Run `homonto update` in each workspace. `onto doctor` / `to doctor`
report version skew until you do.

## Loading the config

Errors name the file, the key, and usually the fix:

| Error contains | Fix |
|---|---|
| `subagents.<name>.opencode model is required` | Add a model block for that agent; every builtin agent a package installs needs one. |
| `declares a model override, but its source "local:…" is not builtin:` | Remove the block; put `model:` in the local agent file. |
| `is a workflow, not an engine` | Move `builtin:h` to `[workflows.h]` (or `onto`/`to`/`no` to `[engines]`). |
| `unknown configuration fields` + `[frameworks.…]` | Rename to `[engines.*]` / `[workflows.*]`. |
| `models.opencode.<tier> is an unknown table` | Model tiers were removed; use per-agent `[subagents.<name>.opencode]`. |
| `is missing required scope` | Add `scope = "project"` or `"user"`. |
| `is tune-only; scope, repo, mode… would be ignored` | A block without `source` tunes an installed agent; remove declaration fields or add a `source`. |
| `settings.opencode key "mcp" is reserved` | Use `[mcps.<name>]` (or `[plugins.opencode.*]` for `plugin`/`plugins`). |
| `OpenCode has no effort setting` | Use `variant`. |
| `targets … claude` / `codex` | Those adapters were removed in v0.13.0; delete the key. |
| `[tmp] takes only dir` | `[tmp]` has one key, `dir`. |

## Applying

**`error: env var X is not set` / pass errors.** A secret couldn't resolve;
nothing was written. Set the variable, install `pass`, or add the entry.

**Conflict on a skill, command, or agent link.** A real file or a foreign
symlink is at the target. homonto never overwrites it. Move it away and
re-apply.

**A tool file is "unparseable".** Fix or restore the JSON; homonto won't
overwrite what it can't parse.

**Comments disappeared from `opencode.jsonc`.** Any apply that rewrites that
file normalizes it to JSON. No-op and link-only applies keep comments.

**Something I added by hand was pruned.** A declared resource that exactly
matched disk was adopted; removing the declaration then prunes it. The TOML
is the source of truth for what it declares.

**Moved the repository; links look broken.** Run `homonto plan` and
`homonto apply` from the new path; eligible links are repaired. If a
conflict remains, inspect the link and `.homonto/state.json`.

**Changed a model, agent didn't change.** Run `homonto apply`. If a very old
`.homonto/catalog` persists, `rm -rf .homonto/catalog && homonto apply --yes`
re-materializes it.

**Plan says `No projection changes; catalog re-materialization pending`.**
Expected after a binary upgrade, a model change, or a `[tmp]`/`[tooling]`
change: generated catalog content must be rewritten but no projected link
moves. Run `homonto apply`; it reports
`No projection changes; catalog re-materialized.`

## Scripting

**Captured nothing.** Output is on stderr; add `2>&1`.

**Exit codes.** `plan --exit-code`: `2` pending. `status --exit-code`: `2`
pending, `3` drift. `onto doctor --quiet` / `to doctor --quiet`: non-zero on
findings.

## onto

**Mutating commands refuse: "has no [engines.onto] table" / "run `homonto apply`".**
Declare `[engines.onto]` and apply. `[workflows.h]` alone doesn't install
onto.

**`advance` refuses.** The message names the gate. `onto gate <change>`
lists every pending decision with its exact command.

**`advance` to close refuses: dirty worktree.** `onto dirt <change>` shows
each path classified. Commit this change's own records and resolve `source`
paths; another change's records never block.

**`close` refuses.** Check in order: phase is `close`; artifacts exist;
`verify-result pass` with one passing `Result:` line; no source commits since
that pass (re-verify, or reopen for a real fix); `onto merge-deltas` done;
`guides`, `integration`, `close-confirmed`, base anchors recorded;
dependencies integrated; clean source.

**`complete-integration` rejects `merge:<sha>`.** It must be a real two-parent
merge containing the verified candidate, reachable from the recorded base
branch. Squash, rebase, and fast-forward merges don't qualify. Merge with
`git merge --no-ff`, or open a PR and record `pr:<url>`.

**Archived change still shows "integration pending".** Record a receipt for
**every** selected repository (`--repo <alias>` each). If archiving was
interrupted, rerun `onto close <change>` first.

**`evidence record`: unknown Scenario-ID.** Declare it once: in a delta spec
under a scenario, or — with no delta files — on its own line in `tasks.md` or
`verification.md`. Delta files live at `specs/<capability>.md`.

**Repeated verify failures.** Three or more failed rounds while still failing
is a doctor finding: decide to accept the deviation or keep fixing.

## to

**Mutating commands refuse.** Declare `[engines.to]` and apply.

**`to done` refuses a scoped change.** Every selected repository must be clean
and Git-readable. Commit or resolve the listed paths, or restore a removed
`[repos]` alias.

**Change is `done` but still in `tasks/`.** An interrupted archive. Re-run
the same finishing command (`to done <name> --verified` or
`to abandon <name>`).

**"another to command is in progress (lock held at …)".** A concurrent
command, or a killed one. A lock whose pid is gone is reclaimed
automatically; a lock with no readable pid must be removed by hand.

## Workspaces

**`worktree create` refuses the base.** It must match the change's frozen
commit **and** target branch. Choose targets at `onto new` / `to new` with
`--base alias=branch`.

**Can't reuse a change name.** An abandoned onto record still in
`changes/<name>/`, or a retained worktree binding, reserves it. Run
`onto cleanup preview <name>` (or `to cleanup preview <name>`), resolve any
blocker it lists, then `cleanup apply <name> --identity <id> --yes`.

**`cleanup apply` refuses.** `ineligible` means the change is still active or
an onto integration receipt is missing: finish or abandon it. `blocked` names
the reason: uncommitted or ignored record files (commit them), a dirty or
unintegrated worktree, or two generations with one name (pass the
`--identity` from `cleanup list`).

**Managed mutation blocked: pending checkpoint.** Fix the Git identity or hook
problem, run `homonto workspace recover`, inspect state, then continue.

**`worktree remove` refuses although `git status` is clean.** Check for
ignored files, submodule changes, `assume-unchanged` / `skip-worktree`
entries, and whether the target branch locally contains the worktree HEAD.

## OpenCode

**No workflow panel or context.** You need OpenCode 2, a builtin
onto/to/h package (or `workflow_context = true`), and a session started
inside the config root. Restart OpenCode after apply.

**GitHub draft tools fail.** They need `[workflows.h]`, OpenCode 2's bound
local service, and native question support; headless clients can't approve.

**`workflow_bridge` — the OpenCode 1 workflow bridge was removed.** Delete the
key (and any `source = "homonto-workflow"` plugin declaration); the V2
`workflow_context` plugin is on by default with onto, to, or h.
