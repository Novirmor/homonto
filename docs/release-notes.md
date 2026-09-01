# Release notes intro

This file is prepended to every GitHub release's auto-generated notes by the
`release` workflow (`--notes-file docs/release-notes.md --generate-notes`), so
every release states the accepted limitations up front. Keep it short; the
per-release changelog is generated automatically below it.

---

## What's in this release

This release ships **three binaries** — `homonto` (config projector), `onto`
(spec-driven workflow operator), and `to` (minimal coding-framework
bookkeeper) — for every supported OS/arch as separate archives under one
`SHA256SUMS`. `onto` and `to` each require `homonto` to have installed their
framework first (`[frameworks.onto]` / `[frameworks.to]` + `homonto apply`).

### New in v0.13.0 — OpenCode-only and designated multi-repo work

- **OpenCode is the sole adapter.** Claude Code and codex configuration now
  fail closed with a migration error instead of projecting stale support.
- **Declared repos are real effect targets.** Repo-tagged project resources
  project into the named declared repository with isolated state and
  per-repository drift reporting; undeclared siblings remain untouched.
- **Scoped workflow gates.** `onto new` and `to new` accept `--repo` aliases.
  Workflow state stays in the config repo, while selected repositories must be
  Git-clean before `onto` close or `to` done can complete.

### New in v0.12.0 — a self-healing workspace lock, honest release notes

A maintenance release over v0.11.0: one user-visible behavior change, two
public-docs corrections (one of which shipped inside every release's notes),
and internal cleanups. Catalog content is unchanged (catalog 0.11.0, onto
0.8.0, to 0.6.0).

- **`to` reclaims a stale workspace lock by itself.** A SIGKILLed command
  used to wedge every mutating `to` command until `docs/tasks/.to.lock` was
  removed by hand. The lock already records its holder's pid; on a conflict
  `to` now probes it, and a pid that provably no longer runs (finished, or
  no such process) means the lock is stale — it is removed and the command
  proceeds. A live pid, a pid owned by another user, and a lockfile with no
  readable pid (a crash between creating the file and writing it) are never
  touched, so a live session's lock cannot be stolen. `homonto apply`'s
  project lock keeps its deliberately manual reclamation.
- **The standing limitations block no longer denies remote frameworks.**
  Every GitHub release carried the claim that remote sources exist for
  subagents only and there are no remote framework sources;
  digest-pinned `remote:` frameworks have been supported and documented all
  along. The block now states the real rule.
- **Release packaging fixes.** `scripts/build-release.sh` falls back to
  `shasum -a 256` when `sha256sum` is absent (macOS runners), keeping
  `SHA256SUMS` byte-identical; and the release checklist's post-tag smoke
  sequence now runs as written (it previously called `onto doctor` in a
  workspace where `onto init` had never been able to run).
- **Internals.** The dead `internal/merge` package (zero importers since the
  projector split) is deleted; the Claude and OpenCode adapters' duplicated
  key/document helpers have one owner in `baseadapter`, which also gains its
  first direct tests; and `config`'s ~180-line `validate` cascade is split
  into per-section helpers with unchanged rules — with one nondeterminism
  removed: skills now validate before commands instead of map iteration
  order deciding which error surfaces.

### New in v0.11.0 — a resumable record, enforced

onto's axis is now stated plainly: a change survives being handed to someone
who was not there. `to` matches onto's code standards and skips its record;
company size was never the distinction and is no longer claimed
([ADR 0021](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0021-onto-is-for-handoff-and-audit.md),
catalog 0.11.0, onto 0.8.0, to 0.6.0).

- **`onto doctor` now reports `tasks.md` ↔ `plan.md` drift** — a task number
  in one file and not the other, or any checkbox in `plan.md`. That pairing is
  the resume mechanism (continue at the first unchecked item, read its detail
  under the matching `## Task N.M`), and it was previously checked only by a
  prose item in the close-phase lint. **A workspace carrying existing drift
  will start failing `onto doctor`, including the `--quiet` enforcement hook.**
  That is the unreported problem surfacing, not a regression. A change with no
  `plan.md` is a preset and reports nothing.
- **Subagent concurrency follows write-scope, not framework**
  ([ADR 0019](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0019-parallelism-follows-write-scope.md),
  [ADR 0020](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0020-onto-parallel-implementers-are-supported.md)).
  `to` previously forbade all subagent parallelism; the justification only ever
  covered agents that *write*, and three of its four never do. Read-only agents
  now run concurrently in both frameworks; the single implementer stays serial
  in `to`, and in onto may run in parallel given a worktree each.
- **Verify's skeptic lenses were renamed** to `abuse`, `data-migration`, and
  `compatibility`. They previously collided with build's reviewer lenses on
  "security" and "contract" despite asking a different question — a reviewer
  reads the diff, a skeptic attacks the running system. Prose-only; nothing in
  the binary keys on lens names.
- **Docs corrected against the shipped binaries.** An audit found and fixed
  nine mismatches, including: the state field is `build_mode` (not
  `execution`); `read_only` renders as Claude's `disallowedTools:` **denylist**
  (not a `tools:` allowlist); the three evidence tokens
  (`proposal_approved`, `approach_confirmed`, `close_confirmed`) were absent
  from both the canonical schema and the command reference; `onto advance
  --to build` was undocumented; onto ships **five** agents, not four (the
  `onto` orchestrator renders for OpenCode only); and `schema_version` is a
  real top-level config key that rejects a newer config fail-closed.
- **Artifact sections have one owner.** Grounding is recorded in `proposal.md`
  (open) and `design.md` (design) and no longer duplicated into `notes.md`;
  scope `Non-Goals` belong to `proposal.md` alone.

### New in v0.10.0 — tooling providers are declared, not shipped

*(Prepared but never tagged; it ships as part of v0.11.0.)*

The `onto` and `to` frameworks no longer name `rtk` and `graphify` in their
shipped prose. Which tools the workflow grounds against is now configuration
([ADR 0022](https://github.com/noviopenworks/homonto/blob/main/docs/adr/0022-tooling-providers-are-declared-not-shipped.md),
catalog 0.10.0, onto 0.7.0, to 0.5.0):

```toml
[tooling]
shell_proxy = "rtk"       # "rtk" | "none"
code_intel  = "graphify"  # "graphify" | "okf" | "none"
```

- **BREAKING for the rendered workflow — both keys default to `none`.** A
  config with no `[tooling]` table now gets a preflight that names no tool at
  all, and grounding falls back to direct file reading (already the documented
  fallback). **Existing configs keep loading unchanged**; only the rendered
  skill text differs. To keep the previous behavior, add the two lines above.
- **`okf` is a selectable code-intelligence provider**
  ([okf-generator](https://github.com/UmairBaig8/okf-generator)). homonto
  **references** it and never downloads, installs, updates, or executes it —
  exactly as it never installed `rtk` or `graphify`. Installing a provider
  stays your job.
- **`homonto apply` generates `references/tooling.md`** inside each framework's
  dispatcher skill, describing exactly the declared pair. Shipped `SKILL.md`
  files defer to it, so a provider you did not declare is never mentioned. The
  file is regenerated on every apply — do not hand-edit it.
- **An unknown key or provider name fails at load**, naming the offender and
  the accepted set (`tooling.code_intel "ctags" is not a known provider
  (accepted: graphify, okf, none)`).
- **`homonto doctor` warns when a declared provider is not detected.** It
  probes `PATH` and index/bundle directories only and never runs the provider,
  so it cannot hang. The finding never fails a projection.

Editing `[tooling]` re-renders on the next apply: the materialize fingerprint
gained a tooling component, so a provider change is no longer invisible to the
"everything up to date" gate.

### New in v0.9.0 — onto's judgment gates are enforced, not documented

`onto` gated artifacts and bookkeeping but took a change's word on the three
decisions that need human judgment, and on whether its verify actually passed.
Those are now enforced in the binary (capability spec
`openspec/specs/onto-evidence-gates/`, onto framework 0.6.0):

- **BREAKING for in-flight `workflow: full` changes — three evidence tokens
  gate the judgment decisions.** `onto advance` refuses to leave `open` without
  `proposal_approved` and to enter `build` without `approach_confirmed`;
  `onto merge-deltas` and `onto close` refuse for **every** workflow without
  `close_confirmed`, checked before any shared file is mutated. Record them
  with `onto set proposal-approved|approach-confirmed|close-confirmed <change>
  "<evidence>"` (free-form, convention `YYYY-MM-DD <summary>`). A change
  mid-flight when you upgrade blocks on its next `advance` until the token is
  recorded — the unanswered gate is simply re-asked. Preset workflows (`fix`,
  `tweak`) are exempt from the two design-phase tokens. Unanswered tokens show
  up in `onto gate --json`.
- **Leaving `verify` cross-checks the report against the state.** The first
  `Result:` line of `verification.md` must agree with `verify.result=pass`, so
  a recorded pass can no longer be self-asserted. `Result: pass (2 accepted
  deviations)` still passes; `Result: passing` does not.
- **`onto state <change> --json` now derives the working phase from workspace
  artifacts** (`derived_phase`, plus `phase_mismatch` when the state file's
  claim disagrees) instead of echoing the claim it is named to distrust.
  `onto status` annotates a disagreeing row with `(working: <phase>)`, and
  `onto state`'s text output does the same. The evidence table lives in tested
  Go rather than being re-run from prose per dispatch.
- **`onto advance <change> --to build` walks a preset from `open` to `build` in
  one call**, running every per-hop gate. It refuses for `workflow: full`
  (those advance one gate at a time), for any target other than `build`, and
  for a change already at or past `build`.
- The onto skills record their token at the gate they own, so an agent
  following the workflow gets this for free; `docs/guides/onto-workflow.md`
  carries the token table and the migration note. Catalog 0.9.0.

### New in v0.8.0 — explicit per-agent models; model tiers removed (BREAKING)

The model tier system is gone (ADR 0016). Model selection no longer routes
through `role:` frontmatter and shared `[models.<tool>.<tier>]` blocks —
every declared subagent names its own model, per tool, where the agent is
configured:

```toml
[subagents.onto-reviewer.claude]
model = "opus"
variant = "1m"
effort = "high"
```

- **BREAKING — `[models.<tool>.<tier>]` blocks are rejected at load**, naming
  the offending table. Delete them and declare a
  `[subagents.<name>.<tool>]` block (non-empty `model`) for every declared
  subagent × targeted tool — framework-expanded agents included. A missing
  block fails at load with `subagents.<name>.<tool> model is required`, so
  the offender is named instead of today's anonymous missing-tier error.
- **BREAKING — homonto no longer manages the main session model.** The
  route-derived default (`model` in Claude settings, `model`/`small_model`
  in OpenCode) is not projected anymore; each tool uses its own default.
  If you pinned the main model through `[models.claude.architectural]`,
  move it to `[settings.claude].model` (or `[settings.opencode].model` /
  `.small_model`) — the explicit settings path is unchanged.
- Catalog subagents no longer carry `role:` frontmatter; a leftover `role:`
  in your own agent files is ignored as unknown frontmatter.
- Tool variants of a subagent now materialize only for the tools it actually
  targets; stale variants of untargeted tools are removed on apply.
- `onto new` proposals now require a Non-Goals section (onto framework).

### New in v0.7.0 — security hardening + deep code-review pass

A full code-quality review of `internal/` found and fixed five HIGH-severity
silent-failure paths, eight maintainability hotspots, and several test gaps.
Shipped happy-path behavior is unchanged; every refactor was verified by the
existing test suite plus 92 new tests (871 → 963). The changes that **are**
user-visible all turn previously-silent bugs into loud errors.

**Trust boundary and exec hygiene:**
- **`git://` is rejected as a remote transport** (insecure, like `http://`
  already was). Use `git+https://`, `git+file://`, `https://`, or `file://`.
- **Every external `exec` (pass, git) is now bounded by a 30s timeout.** A
  hung gpg-agent passphrase prompt or a git credential prompt previously hung
  the whole CLI indefinitely; you can now Ctrl-C through it.
- **`context.Context` is threaded through `engine.Build`/`Apply`**, so a hung
  remote fetch is interruptible from the calling CLI.

**Loud errors where silence was a bug:**
- **A malformed `homonto:` frontmatter block now fails the projection** with
  a named parse error. Previously it was treated as "no block" and the agent
  was silently projected with no model line and default permissions.
- **A corrupted TOML tool file now fails the projection** rather than being
  folded into "key absent" — the previous behavior could emit a misleading
  "create" plan or report false drift.

**Maintainability (no behavior change):**
- New `internal/adapter/baseadapter` absorbs ~590 LOC of byte-identical
  methods between the Claude and OpenCode adapters; both adapters shrink by
  ~294 LOC each.
- New `internal/resourcepath` unifies the three former
  `skillpath`/`commandpath`/`subagentpath` packages (their switch bodies had
  drifted in subtle ways).
- New `internal/workcli` extracts the gate / `validChangeName` /
  `ErrQuietFindings` scaffolding shared between `ontocli` and `tocli`; the
  `"0.1.0-dev"` literal now lives in one place (`buildinfo.DevVersion`).
- The 1381-line `internal/config/config.go` god file is split into four
  focused files (`config.go` types / `load.go` decode+migrate+Load /
  `validate.go` validation / `expand.go` framework expansion).
- Three near-identical `doctor{Skills,Commands,Subagents}` methods collapse
  into one `doctorResource(tool, doctorOp)`.
- `internal/schema.ErrTooNew` is the shared sentinel for the five
  schema-version-too-new checks (state, config, onto-state, catalog builtin,
  catalog local) — callers can `errors.Is(err, schema.ErrTooNew)` instead of
  substring-matching.
- Error wrapping at six sites uses `%w` (was `%v`) so error types survive
  the engine boundary; catalog loader surfaces `fs.ReadDir` errors instead
  of treating an unreadable directory as missing.

### New in v0.6.1 — lossless per-tool agent rendering

An audit of the rendered agents against both tools' real contracts found
and fixed four silent information losses (catalog 0.6.0, onto 0.4.1,
to 0.3.1):

- **Claude renders a denylist, not an allowlist.** The old `tools:`
  allowlist silently stripped every unlisted default (WebFetch, WebSearch,
  Skill, …) that the OpenCode variant kept. Claude now gets
  `disallowedTools:` covering exactly the denied intent — read-only denies
  `Edit, Write, NotebookEdit`, `bash: false` denies `Bash`, `spawn: []`
  denies `Agent`/`Task` — matching OpenCode's deny-by-exception model.
- **`steps` now reaches Claude as `maxTurns`** (it was dropped as
  "no concept"; Claude has one).
- **`dialogs: false` is now enforced in OpenCode** (`question: deny`);
  omitting the line left the question tool available in defiance of the
  declared intent. All eight specialist subagents in both frameworks are
  now `dialogs: false` — matching the protocol's "a subagent never prompts
  the user; it returns a `Questions:` section" rule, which is also the only
  behavior Claude can express (AskUserQuestion is never available to Claude
  subagents). The onto orchestrator (primary) keeps its dialogs.
- **The unrecognized `mode:` line is gone from Claude variants** (Claude
  has no such frontmatter field).

### New in v0.6.0 — four model tiers, project-scoped model settings & MCPs, closed tier names

**`review` is the fourth model tier.** Model routes are now `architectural`
(orchestrate/design), `coding` (implement), `review` (judge others' work),
and `trivial` (cheap lookups) — and a model-backed config must declare all
four per enabled tool (**breaking**: existing three-route configs fail at
load until a `[models.<tool>.review]` block is added). The onto and to
reviewers and skeptics now run on the review tier instead of borrowing the
architectural one, in both Claude Code and OpenCode; the catalog is bumped
to 0.5.0 and re-materializes on the next apply.

**Route-derived default-model keys follow scope.** When every model-backed
resource (framework, command, subagent) enabled for a tool is
project-scoped, the `[models.<tool>.*]`-derived default-model keys now
project into the project-level config the tool merges over its global one
(`<repo>/opencode.jsonc` `model`/`small_model`;
`<repo>/.claude/settings.json` `model`) instead of the global file — one
repository's workflow models no longer become every other session's
defaults, and two repositories no longer fight over the same global keys.
Previously-applied global keys are pruned automatically on the next
`apply`. Any user-scope model-backed resource, and all explicit
`[settings.<tool>]` keys, keep today's global projection.

**MCP servers take a `scope`.** `[mcps.<name>] scope = "project"` projects
the server into the project-level config (Claude Code `<repo>/.mcp.json`;
OpenCode `<repo>/opencode.jsonc`) instead of the global one, so a
repository's servers no longer run in every other session. Default stays
`user` (global, today's behavior); codex remains user-scope only and a
project-scoped codex target fails at load. A previously-global server whose
scope changes migrates automatically on the next `apply`.

**Tier and role names are enforced.** `[models.<tool>.<level>]` with a
level outside `architectural`/`coding`/`trivial` now fails at load naming
the offender, and an agent frontmatter `role:` outside the same three tiers
fails at render — both were silent no-ops before (an unknown role rendered
the agent with no model at all).

### New in v0.5.1 — documentation rewrite

Docs only; the binaries are identical to v0.5.0. The README and every living
guide were rewritten for accuracy and directness: the source matrix is now
stated correctly everywhere (frameworks accept `builtin:`, `local:`, and
digest-pinned `remote:`; onto and `to` are mutually exclusive), stale
"`to` is planned" claims are gone, and the reference guides were re-checked
against the shipped binaries' command surfaces.

### New in v0.5.0 — live task lists, hardened `to`, principle guides

**The task list is live state — in both frameworks.** Discovered work is
appended to the checklist (with its files and verification) *before* its code
is written; checkoffs ride each task's own commit; tasks are never renumbered
or deleted (`SUPERSEDED` instead), so a fresh session always resumes from the
first unchecked task. onto gets this in onto-build, its templates, the
presets, and the subagent protocol (implementers report discovered work, the
coordinator appends it); `to` gets the same discipline adapted to its plan
contract.

**`to` grew teeth without growing ceremony:**

- **Plan contract**: every task carries `Files:` / `Change:` / `Verify:`
  fields plus a whole-change `Final Verify:` line; notes and verification
  evidence live in the same archived `plan.md`. `to doctor` diagnoses
  contract violations (line-numbered), wedged archives, and version skew;
  `--quiet` is the enforcement hook primitive.
- **Crash convergence**: an interrupted `done`/`abandon` no longer wedges a
  change — re-running the same command completes the archive.
- **Date-prefixed archives** (`docs/tasks/archive/<date>-<name>/`) free
  change names for reuse; mutating commands take a fail-fast workspace lock.
- **`to done --evidence "<text>"`** records what was asserted, verbatim and
  unchecked, so a real verification is distinguishable in the archive.
- **`to handoff`** now excerpts what a resuming session needs: the plan head,
  every unchecked task contract, `Final Verify:`, and bounded notes.

**Docs**: the `to` guides split into [workflow concepts](https://github.com/noviopenworks/homonto/blob/main/docs/guides/to-workflow.md)
and a [command reference](https://github.com/noviopenworks/homonto/blob/main/docs/guides/to-reference.md)
(mirroring onto's pair), and two principle guides —
[YAGNI](https://github.com/noviopenworks/homonto/blob/main/docs/guides/yagni.md) and
[KISS](https://github.com/noviopenworks/homonto/blob/main/docs/guides/kiss.md) —
map where each framework structurally enforces building only what's needed,
simply. Framework versions: onto 0.3.2, to 0.2.0; catalog 0.4.0.

### New in v0.4.0 — the `to` framework

`to` is the minimal coding framework for LLMs: **plan → do → done**, a
bookkeeper binary (`init`, `new`, `status`, `phase`, `done --verified`,
`abandon`, `handoff`; structured `--json` output on each of those workflow
commands), and the `builtin:to` catalog
framework — a `/to` dispatcher, three phase skills, a vendored `to-no-slop`,
and four **sequential-only** specialist subagents adapted from onto. Changes
live under `docs/tasks/` and archive on done. Design and rationale:
`docs/to-framework-design.md`.

Two deliberate properties to know before adopting it:

- **onto and `to` are mutually exclusive.** Declaring both frameworks in one
  `homonto.toml` fails at load — pick one workflow per repository (onto for
  evidence-gated enterprise changes, `to` for simple development). There is no
  escalation path between their state formats.
- **`to done --verified` is self-asserted.** The binary records the checkbox;
  it observes no evidence. The verification rigor lives in the `to-done`
  skill (real verify run + a single adversarial skeptic pass), not in a gate.

### Breaking in v0.3.0 — comet, openspec, and superpowers removed

The catalog now ships **only homonto-native frameworks**: `onto` (and, since
v0.4.0, `to`) — plus the loose framework-agnostic
skills/commands (`handoff`, `grilling`), which are a separate channel and
unaffected. A config declaring `[frameworks.comet]`, `[frameworks.openspec]`,
`[frameworks.superpowers]`, or `builtin:comet-navigator` now fails at load
with `catalog: unknown framework` / `unknown subagent`; remove the
declaration (their projected links are pruned on the next apply) or vendor
the content yourself via a `local:` framework / pinned `remote:` source.
v0.2.2 is the last release carrying them. Rationale: ADR 0015.

### New in v0.2.2 — dirty-workspace support

The close gate no longer treats every uncommitted path the same. `onto dirt
[change] [--json]` classifies each dirty path — `own` (the change's own
`docs/changes/<name>/` evidence), `change` (another change's docs), `source`
(everything else) — and `onto advance`/`onto close` now tolerate `change`
dirt: one change's in-flight artifacts no longer deadlock another change's
close. What does block (`own` + `source`) is listed right in the refusal
instead of a bare "dirty worktree blocks close". The onto skills gained a
dirty-workspace protocol (attribution stays with the agent; the binary owns
classification).

### Fixed in v0.2.1 — deep-review findings

**onto's terminal states are now actually terminal.** An abandoned change could
archive as a success, have its evidence tokens forged via `onto set`, and merge
its never-accepted deltas into the living specs; all three paths now refuse.
`merge-deltas` recovers from a crash between its per-file writes instead of
wedging the change forever; `onto scale` errors without a recorded base ref
instead of silently measuring an empty diff as "light"; dependency resolution
is an exact name match (dep `auth` is no longer satisfied by an archive named
`…-refactor-auth`); a close crash can no longer leave `archived: true` at the
original path; `doctor` skips abandoned changes and `--quiet` is now fully
quiet.

**homonto re-materializes when framework CONTENT changes.** Editing a `local:`
framework's resources — or repinning a `remote:` framework's digest, which is
how a patched resource ships — used to be ignored forever ("No changes"). The
materialize gate now digests source content. Related: `plan` surfaces a pending
re-materialization (text + `--exit-code` 2) instead of disagreeing with apply;
renamed/de-declared resources are GC'd from `.homonto/catalog/` instead of
lingering where the adapters' variant-preference could resurrect them; and a
per-subagent model override is validated no matter what the entry's `targets`
say (an unvalidated value could previously reach a live agent file), with
conflicting overrides for one builtin now a deterministic load error.

### Breaking in v0.2.0 — `effort` and `variant` now do something

They were **required by validation and projected nowhere**: homonto forced you
to write two fields it then discarded — and never checked, so real configs
filled up with values no tool accepts (`effort = "normal"`, `variant = "max"`,
even `effort = "n"`). Now they are **optional, validated, and actually
projected** into each tool's own dialect:

| | Claude Code | OpenCode |
|---|---|---|
| `variant` | rendered *into* the model string (`opus[1m]`); **alias-only**, `1m` is the only documented one | a first-class `variant:` field, any provider-defined value |
| `effort` | a real field: `low`, `medium`, `high`, `xhigh`, `max` | **no such concept** — declaring it is now an error |

**You may need to edit your config.** A route naming just a `model` is now
complete, so the simplest fix is to delete values you were only writing to
satisfy the old rule. Otherwise the loader tells you exactly what is wrong:

```
parse config: models.claude.coding effort "normal" is not a Claude effort level (low, medium, high, xhigh, max)
parse config: models.opencode.coding sets effort "high", but OpenCode has no effort setting — use variant, or drop it
```

**New:** retune one agent without restating its tier — each field wins field by
field, and no `source` is needed for an agent a framework installed:

```toml
[subagents.onto-skeptic.claude]
effort = "max"
```

### Breaking in v0.2.0 — onto's subagents are namespaced `onto-*`

Every resource the onto framework ships is now namespaced, so installing onto
cannot collide with another framework's — or your own — agent of the same
generic name. Two builtin subagents were renamed:

| Old | New |
|---|---|
| `builtin:code-reviewer` | `builtin:onto-reviewer` |
| `builtin:codebase-explorer` | `builtin:onto-explorer` |

If you declare either **standalone** in a `[subagents.*]` table, update its
`source` — an old name now fails at load with `catalog: unknown subagent`. If
you install them via `[frameworks.onto]`, apply handles the rename for you: the
old agent files are pruned and the new ones projected. (The onto skills, its
commands, and the `onto` dispatcher itself are unchanged; `onto` is the
namespace root.)

### Fixed in v0.2.0 — subagents now track their model routes

Changing a `[models.<tool>.<role>]` route did **not** re-render the subagents
stamped from it. The projected agents stayed frozen at the model they were first
materialized with, while the tool's own `setting.model` — re-read from the routes
on every apply — moved correctly: one config, two different answers. If you have
edited a model route since installing a framework or subagent, **upgrade and run
`homonto apply`** to re-stamp your agents; verify with
`grep '^model:' .homonto/catalog/subagents/*.md`.

Three related defects went with it: a deleted rendered agent variant is now
restored instead of stranding a dangling symlink that `plan`/`status`/`doctor`
all called healthy; `apply` now re-materializes the catalog even when the
projection plan is empty; and `doctor` no longer reports a permanent, unfixable
finding for an OpenCode-primary agent's by-design absent Claude variant.

## Known limitations

homonto is a young, deliberately narrow tool. For the current 0.x line:

- **OpenCode JSONC comments are not preserved** on any apply that writes
  `opencode.jsonc` (the file is rewritten as normalized JSON). Accepted for beta.
- **`import` is a narrow Claude MCP bootstrap** — Claude global MCP servers only,
  best-effort secret redaction, no skills/plugins/settings/OpenCode import.
- **The bundled catalog ships only homonto-native content**: the `onto` and
  `to` frameworks (mutually exclusive) plus the loose framework-agnostic
  skills/commands. Third-party frameworks are not bundled; vendor them via a
  `local:` path or a digest-pinned `remote:` archive (the same fail-closed
  verification `remote:` subagents use). Every `remote:` source requires a
  `digest = "sha256:…"` pin, and homonto never re-resolves a pin to newer
  content on its own.
- **OpenCode is the only adapter.** Claude Code and codex support was removed
  in v0.13.0; a config naming them fails at load naming the key.
- **Secrets require `pass` or an env var** at apply time (`${pass:...}` /
  `${ENV_VAR}`).
- **Moving or renaming the repo** breaks skill symlinks (absolute targets):
  delete the stale links and re-apply.

See the README's "Caveats" section and
[`docs/guides/troubleshooting.md`](https://github.com/noviopenworks/homonto/blob/main/docs/guides/troubleshooting.md) for details.
