# Troubleshooting & caveats

Known limitations of the beta line, common gotchas, and their workarounds.

## Building & installing

**`go build .` fails with `build output "homonto" already exists and is a
directory`.** The output name collides with the `homonto/` content directory
next to `main.go`, and `go build -o homonto .` silently deposits the binary
*inside* that directory. Use `go install .`, `go run .`, or build to an
explicit path outside the content dir:

```bash
go build -o ./bin/homonto .
```

**Version prints empty or wrong after `go install`.** Release builds stamp
the version at link time:

```bash
go install -ldflags "-X github.com/noviopenworks/homonto/internal/cli.Version=1.2.3" .
```

**Installed a newer binary but tools still have old content.** Installing a
binary does not touch projected content. Run `homonto update`; it
re-materializes the embedded catalog at the running version and re-projects
it. `onto doctor` and `to doctor` report a **version skew** finding when a
workflow binary and the homonto that installed its framework have drifted
apart.

**I changed a model but my agents still show the old one.** Fixed in
v0.2.0. A subagent's `model:` is stamped from its configured model block
(today `[subagents.<name>.<tool>]`; tier routes before v0.8.0) at
materialization, and materialization used to be gated on the catalog version
alone — so a route change left the rendered agents frozen while the tool's
own `setting.model` moved, giving two different answers from one config.
Upgrade and run `homonto apply`. On an older binary, force a
re-materialization:

```bash
rm -rf .homonto/catalog && homonto apply --yes
```

## Scripting

**"My script captures nothing."** homonto, onto, and to print through
cobra, which writes to **stderr**. Redirect with `2>&1`.

**Exit codes.** By default commands exit `0`/non-zero. The richer taxonomy
is opt-in: `plan --exit-code` exits `2` on pending changes; `status
--exit-code` exits `2` on pending and `3` on drift. `--output json` on
`plan`, `status`, and `doctor` gives machine-readable output; on the onto
side, `state --json`, `gate --json`, `scale --json`, and `graph --json` do,
and to's workflow commands take `--json`.

## Projection

**OpenCode comments disappeared.** OpenCode's `opencode.jsonc` supports
comments — and any apply that *writes* that file rewrites it as normalized
JSON, removing all comments. A skills-only or otherwise no-op apply does not
write the file, so comments survive those. Accepted for beta.

**"Conflict" reported on a skill or subagent link.** homonto never clobbers
a file that is not its own symlink. A real file, or a link pointing
elsewhere at the target path, is reported instead of overwritten. Move the
conflicting file out of the way and re-apply.

**I moved/renamed my homonto repo and now everything conflicts.** Skill
symlinks store an **absolute** target, so after a move the existing links
point at the old path, and `apply`/`status` report conflicts rather than
silently repointing — homonto never changes a symlink it cannot prove it
owns. Delete the stale links and re-run `apply` to relink at the new
location.

**A tool file was reported unparseable.** That adapter aborts and reports;
homonto never overwrites a file it cannot parse. Fix the JSON by hand (or
restore it) and re-apply. The other tool's apply is unaffected.

**Something I configured by hand got pruned.** Only resources homonto
recorded in state are ever pruned — but note that a declared resource
matching disk is *adopted* into state (see
[projection & state](projection-and-state.md)), after which removing it
from `homonto.toml` removes it from the tool too. That is the contract: the
TOML is the source of truth for everything it declares.

## Secrets

**`apply` aborts with a resolution error.** `${pass:…}` needs
[`pass`](https://www.passwordstore.org/) on `PATH` (and the entry present);
`${ENV_VAR}` needs the variable set at apply time. Nothing was written —
apply resolves all secrets before touching any file. `homonto doctor` flags
a missing `pass`.

## Scope of the adapter

**OpenCode is the only adapter.** Claude Code and codex support was removed
in v0.13.0: a config naming them — `targets = ["claude"]` or
`targets = ["codex"]`, `[settings.claude]`, `[plugins.claude.*]`,
`[marketplaces.claude.*]`, or a `[subagents.<name>.claude]` block — fails
at load, naming the key. The `homonto import` command (a Claude MCP
bootstrap) went with them.

- **Frameworks** resolve from the builtin catalog (`onto` or `to`, mutually
  exclusive), a `local:` root, or a digest-pinned `remote:` source.
- **Remote sources** (subagents and frameworks) require a
  `digest = "sha256:…"` pin (see
  [remote source trust](remote-source-trust.md)). homonto never re-resolves
  a pin to newer content; updating is a config edit you make.

## onto

**`onto new`/`advance`/`close` refuse to run.** The mutating commands
require the onto framework to be installed *by homonto*
(`[frameworks.onto]` + `homonto apply`). The read-only commands (`status`,
`state`, `gate`, `scale`, `graph`, `handoff`, `dirt`, `doctor`, `version`)
always work.

**`advance` fails.** The error names the gate: a missing artifact for the
current phase, an unset evidence token (`isolation` before build), an
unchecked `tasks.md` item leaving build, or a dirty worktree entering
close. Run `onto gate <change>` to see the pending decision(s) and the
exact `onto set` that records each one.

**`close` fails.** Check, in order: the change is at phase `close`;
all cumulative artifacts exist; `verify.result == pass` and the report has one
canonical result line; no source commit landed after that pass (close names
the path — re-verify and record a fresh pass, or reopen for a real fix);
`close.merged == true` with a matching receipt (run `onto merge-deltas`);
`base_ref`, `base_branch`, and integration are recorded; the current branch is
a change branch, not `base_branch` itself; guides resolved (full workflow);
every `deps` entry completed; config repo and every selected `--repo` worktree
clean and Git-readable.

**`complete-integration` rejects the receipt.** A merge receipt must name a
real `--no-ff` merge commit that contains the recorded source commit and is
reachable from the recorded base branch. Squash merges, rebased-and-merged
branches, and fast-forwards all fail this by design — the audit trail would
not identify what landed. Merge the change branch again with
`git merge --no-ff`, or open a PR and record `pr:<url>` instead.

**An archived change still appears at close.** Its Git integration is pending,
incomplete for some repository, or its `.onto/integration.json` is invalid.
Complete the recorded local merge (`git merge --no-ff <change-branch>` into
`base_branch` — the receipt must name that real merge commit) or open the
recorded PR, then run `onto complete-integration <change> --receipt
merge:<commit>` or `--receipt pr:<https-url>` per repository — once for the
config repository and once per selected sibling with `--repo <alias>`; the
change derives `done` only when all of them are complete. If archival stopped
before the state flag was written, rerun `onto close <change>` first. `onto
doctor` prints the applicable recovery command.

**"dirty worktree blocks close."** The error lists the first few offending
paths; `onto dirt <change>` shows all of them, classified. Paths under
*another* change's `docs/changes/<other>/` never block (they are that
change's obligation). What blocks is this change's own uncommitted
artifacts and any uncommitted source path. For a scoped change, every dirty
selected sibling path blocks too and the error labels its repo. Commit what
belongs to the change, stash or attribute what doesn't, and retry.

**Repeated verify failures.** `onto set verify-result fail` increments a
counter; at ≥3 rounds `onto doctor` reports it. The workflow expects a
human decision at that point (accept the deviation or keep fixing), not an
endless loop.

**Recovering after context compaction.** `onto handoff <change>` emits a
compact recovery pack (`--write` persists it) so a fresh agent session can
resume without re-deriving state.

## to

**`to init`/`new`/`phase`/`done`/`abandon` refuse to run.** Same rule as
onto: the mutating commands require the to framework installed *by homonto*
(`[frameworks.to]` + `homonto apply`). The read-only commands (`status`,
`handoff`, `doctor`, `version`) always work.

**"[frameworks.onto] and [frameworks.to] are mutually exclusive."** By
design: one workflow framework per repository. Remove one declaration and
re-apply; the removed framework's projected content is pruned.

**`to done` refuses a scoped change.** A change created with `to new --repo
<declared-name>` cannot finish until the config repo and every selected repo
are clean and Git-readable. Commit or stash the listed repo, restore a removed
`[repos]` alias, then run `to done` again. `to abandon` remains available.

**A change shows a terminal phase but still sits in `docs/tasks/`.** An
interrupted finish (a crash between the state write and the archive move).
`to doctor` reports it; re-run the same finishing command
(`to done <name> --verified` or `to abandon <name>`) to complete the
archive.

**"another to command is in progress (lock held at …)".** A concurrent
session holds `docs/tasks/.to.lock`, or a killed process left it behind. The
file records the holder's pid; when that pid is provably no longer running,
the next mutating `to` command reclaims the lock itself. A lock with no
readable pid (a crash between creating the file and writing it) is the one
case for hand removal — a live session's lock is never stolen.
