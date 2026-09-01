# Development plan — v0.1.8 → v0.2.0 (delivered)

> **This plan is history.** Everything below shipped across releases v0.1.8 →
> v0.2.2; it is kept for the rationale behind each decision, not as a list of
> pending work. For where things stand now, read the next section first.

## Status after v0.4.0 (2026-07-18)

**Delivered.** The `to` framework shipped in v0.4.0: the third binary
(`cmd/to`, plan → do → done, JSON output on its workflow commands, git-blind,
gated bootstrap),
the `builtin:to` catalog framework (dispatcher + three phase skills,
`to-no-slop`, four subagents), the onto-xor-to config
exclusivity, and the `to-lifecycle` Docker E2E suite in the release gate.
Design record: [to-framework-design.md](to-framework-design.md).

### `to` fix list (post-v0.4.0)

Reviewed 2026-07-18. Scope rule for every item: `to` is lightweight **by
design** for repos that already run homonto — fixes below harden what shipped
without adding ceremony. The deliberate structural choices (self-asserted
`--verified`, one sequential skeptic, no escalation path, gated bootstrap) are
not on this list; they are the product.

**Status 2026-07-18: items 1–7 are fixed on main** (crash convergence,
date-prefixed archives with same-day suffixing, `to doctor --quiet`,
handoff excerpting head + unchecked tasks, the workspace lock, the
`to-reference` guide + doc index updates, and the optional `--evidence`
flag). Item 8 (live-skill exercise) remains deferred to the testing pass.

**P1 — defects.**

1. **`done`/`abandon` crash convergence.** Both save terminal state, then
   rename into `docs/tasks/archive/`. A crash (or an archive-name collision)
   between the two leaves a terminal change in the active tree that every
   command refuses to touch — wedged with no recovery command. Same defect
   class onto fixed in v0.2.1. Fix: re-running `done`/`abandon` on a
   terminal-but-active change completes the archive move (idempotent
   finish), and the collision check runs before the state write.
2. **Archive names are consumed forever.** `to new` refuses any name present
   in the archive and archive dirs are not timestamped, so a recurring chore
   name (`update-deps`) works once per repo. Fix: archive as
   `<date>-<name>` (onto's convention), free the name for reuse.

**P2 — robustness.**

3. **`to doctor [--quiet]`.** No health check exists, so there is no hook
   primitive — the [enforcement](guides/enforcement.md) recipes cannot apply
   to `to` at all. Minimal doctor: wedged terminal-active changes (item 1),
   state-file validity, `plan.md` present, plan-checkbox contract (the
   `to-do` resume logic depends on `- [ ]` lines the binary never checks),
   and binary↔materialized-framework version skew. `--quiet` = exit-code
   only, mirroring `onto doctor`.
4. **`handoff` truncates the wrong end of the plan.** The excerpt is the
   first 60 lines, but during `do` the unchecked tasks sit at the bottom —
   a long plan hands off its finished history and cuts its remaining work.
   Fix: excerpt = plan head (goal) + every unchecked `- [ ]` line.
5. **No mutating-command lock.** Two concurrent sessions can interleave
   `phase`/`done` on one change (writes are atomic; last-writer wins
   silently). Reuse the applylock pattern for `to`'s mutating commands.

**P3 — docs and decisions.**

6. **User-facing docs.** `to` is documented by its design record and the
   release notes only; `getting-started`, `cli-reference`, and
   `troubleshooting` cover the other two binaries. Add a `to` quickstart +
   CLI reference section, and mention `to` on the README start-here path.
7. **Decide: optional evidence string on `done --verified`.** An optional
   `--evidence "<text>"` recorded verbatim in the archived state would make
   real and skipped verification distinguishable after the fact, at zero
   added ceremony (it stays optional). Maintainer decision — the mandatory
   variant was considered and rejected in the design interview.
8. **Live-skill exercise (deferred by decision).** The mechanical layer is
   E2E-tested; the skills have not yet driven a real agent session
   end-to-end. Planned as part of the future testing pass, not scheduled
   here.

## Status after v0.3.0 (2026-07-15)

**Delivered.** The whole v0.1.8 → v0.2.0 plan below (releases v0.1.8–v0.1.15
and v0.2.0), plus: v0.2.1's deep-review fixes (onto's terminal states, the
content-fingerprint materialize gate, override validation), v0.2.2's
dirty-workspace support (`onto dirt`, classified dirt, the close carve-out for
other changes' in-flight docs), and v0.3.0's catalog narrowing — the bundled
catalog now ships only homonto-native content
([ADR 0015](adr/0015-ship-only-onto-frameworks.md)).

**Known open — not scheduled, each needs a maintainer decision:**

- **The `to` framework.** ~~A second native framework is planned; its scope
  and content are unspecified. Nothing is built.~~ **Shipped in v0.4.0** —
  see the status section above and its fix list.
- **A dedicated `[hooks]` resource** (v0.2.0 item 1's remainder). The feasible
  parts shipped — `onto doctor --quiet` plus the Claude `settings.json` and
  OpenCode plugin recipes in [enforcement](guides/enforcement.md). Auto-shipping
  onto's guard to both tools needs an OpenCode **JS plugin** (no declarative
  hook config exists) whose *execution* cannot be tested in this environment.
  Environment-gated, not undone.
- **Real-tool E2E in CI** (v0.2.0 item 2). `test/e2e/` drives actual Claude
  Code + OpenCode locally; wiring it into CI needs GitHub **secrets** for live
  models. The render invariants it asserts already run on every push through
  the Docker E2E (`homonto-expanded`).
- **Dogfooding onto in this repository.** Deferred to v1 by decision; this
  repo is developed directly on branches, with no external workflow stack
  (see [personas](personas.md)).



Written 2026-07-14 from three analyses: onto-vs-comet gap review, the
subagent/dialog/tool-parity method, and flow-correctness findings. One release
per section, ordered so each ships alone and each unblocks the next. The method
underneath everything: **declare intent once, tool-neutrally; render each tool's
native dialect at projection time; parity tiers are explicit; behavior that can
live in the `onto` binary lives there** (identical everywhere by construction).

Status legend: each item lists Goal / Changes / Acceptance. A release ships only
behind the full gate (unit + `-race` + E2E) like everything else.

---

## v0.1.8 — Flow correctness: task lists at the right time

**Problem.** `onto new` scaffolds `tasks.md` at birth and the open-exit gate
requires it — task decomposition before any design exists. onto-design then says
"update tasks.md if the design…" (draft-then-patch, backwards). Bonus defect
found while grounding this: presets (fix/tweak) can never mechanically reach
`close` — leaving `design` demands a `design.md` presets never write (the "N2
residual").

**Design.** Workflow-aware artifact gates:

| Leaving | full | fix / tweak |
|---|---|---|
| `open` | `proposal.md` | `proposal.md`, `tasks.md` (open-lite checklist is the right time) |
| `design` | + `design.md`, **`tasks.md`** (derived from the confirmed design) | *(pass-through: no design.md demanded)* |
| `build` | + `plan.md`, all tasks checked | all tasks checked (no plan.md) |
| `verify` | + `verification.md` | + `verification.md` |

Empty/unknown workflow = full (strictest, matches closeEvidenceGate).

**Changes.**
- `internal/ontostate/state.go`: `RequiredArtifacts(phase, workflow)`;
  `ValidateSkeleton` passes the loaded workflow.
- `internal/ontocli/advance.go`: pass `st.Workflow`.
- `internal/ontocli/new.go`: scaffold `tasks.md` only for presets; full scaffolds
  `proposal.md` only.
- Skills: onto-open stops drafting tasks (gate reviews proposal only);
  onto-design gains an explicit "derive tasks.md from the confirmed approach"
  step (template stays at `onto-open/references/tasks.md`, cross-referenced);
  dispatcher derivation row `proposal + tasks → design` drops the tasks conjunct.
- `test/docker/onto-lifecycle.sh`: create tasks at the design exit, not at new;
  add a preset leg that advances fix open→…→close mechanically (regression for
  the N2 fix).
- Catalog bump.

**Acceptance.** Full change cannot leave design without tasks.md; preset reaches
close via `onto advance`/`onto close` only; all suites green.

---

## v0.1.9 — Real subagent integration (neutral intent → capability-aware render)

**Problem.** v0.1.3–4 shipped enforced read-only agents but: no implementer
agent (`build-mode subagent` has nothing to dispatch to), the `coding`/`trivial`
model routes are dead config for agents, no delegation-topology enforcement, and
commands/agents can't use either tool's native powers (verified: OpenCode
`permission.task: deny` removes the task tool; project commands honor `agent:`).

**Design.** Extend the `homonto:` neutral block and render per tool at
materialize time **with config in hand**:

```yaml
homonto:
  role: coding        # → Claude `model: sonnet` / OpenCode `model: <route>` from [models.<tool>.coding]
  read_only: false    # existing
  bash: true          # existing
  dialogs: true       # existing
  spawn: []           # [] → Claude: omit Task from tools; OpenCode: task: deny  (full parity)
                      # [a,b] → OpenCode task globs (enforced); Claude advisory  (approximate)
  primary: true       # OpenCode: mode: primary + steps; Claude: SKIP render (entry stays /onto)
  steps: 60
```

Parity tiers are explicit; the renderer skips rather than mis-renders.

**Changes.**
- `internal/agentfm`: v2 schema + `RenderContext{Routes, AgentNames}`;
  `MaterializeSubagents` receives the context from the engine (which has Cfg).
- Catalog: new `onto-implementer` (role: coding, read_only: false, spawn: []),
  new `onto` primary agent (OpenCode-only render; dispatcher prompt;
  `spawn: [onto-implementer, onto-explorer, onto-reviewer]`); explorer and
  reviewer gain `role:` tiers.
- Command rendering: generalize the per-tool variant mechanism to commands so
  `/onto` in OpenCode carries `agent: onto` (routes into the primary agent);
  Claude keeps its dialect untouched.
- Skills: onto-build's `build-mode subagent` path dispatches the implementer
  (spec handoff → diff back → reviewer pass); **subagents-never-prompt
  protocol** (they return a `Questions:` section; the orchestrator asks) — fixes
  the Claude asymmetry where Task subagents cannot prompt mid-run.
- Tests: conformance fixtures asserting both renders per intent + the semantic
  claims ("implementer cannot spawn" holds in both outputs); E2E asserts live
  invariants via `opencode debug agent` (edit/task/question) and the Claude
  variant's `tools:` line.

**Acceptance.** `onto set build-mode subagent` has a working target in both
tools; agent models differ by role per the user's routes; topology mechanical in
OpenCode and Task-omitted in Claude; all invariants in CI, not hand-checked.

---

## v0.1.10 — Gates as dialogs + discipline depth

**Problem A (gates).** Every `> **GATE:**` block is free prose — inconsistently
asked, silently skippable, answers recorded (if at all) in notes.md prose.

**Problem B (coding disciplines).** Comparing onto against the superpowers skill
set it absorbed: the absorption is 30–50:1 lossy exactly where discipline holds
under pressure — TDD (371 lines → ~6: the rationalization defenses are gone),
systematic debugging (296 → ~8: the phased method and 3-failed-hypotheses
escalation are gone), **receiving-code-review (213 → nothing — and load-bearing
since v0.1.3 piped the reviewer subagent's findings back to the orchestrator,
which now implements them unexamined)**, and worktree mechanics (202 → the
recorded choice with no how). Onto is *stronger* than superpowers on
verification (a gated phase), requesting review (an enforced read-only agent),
and subagent execution (a real protocol reference) — the gap is specifically the
four above. Structural cause: comet *composes* superpowers (loads the deep skill
at the moment of need); onto inlined summaries for self-containment.

**Design.** The binary owns the gate schema; skills only render it. For the
disciplines, use onto's own ADR 0006 reference-file mechanism — **vendor the
deep protocols as `references/*.md`** loaded on demand (the onto-no-slop /
subagent-protocol pattern), no dependency on superpowers, self-containment kept.

**Changes.**
- `onto gate <change> [--json]`: emits the pending gate — id, question, short
  header, options (with a recommended default), and which `onto set` records the
  answer. Pure read; derived from phase + state.
- Recorded answers become state (reuse existing setters; add
  `onto set decision <change> <gate-id> <choice>` for confirm-only gates).
- Skills: gates render through AskUserQuestion (Claude) / question tool
  (OpenCode) from the emitted schema; free-prose gate text shrinks to intent.
- Vendored discipline references (prose-only, one catalog bump):
  - `onto-build/references/receiving-review.md` — verify each reviewer-subagent
    finding against the code before implementing; evidence-based pushback; no
    performative agreement. **Highest priority: closes the loop v0.1.3 opened.**
  - `onto-build/references/tdd-protocol.md` — full red/green discipline,
    watch-it-fail-for-the-right-reason, never weaken a test, the rationalization
    table. (`tdd-mode: tdd` is onto-fix's mandatory default; its enforcement
    prose is currently ~6 lines.)
  - `onto-build/references/debugging-protocol.md` — phased method (reproduce →
    whole error → recent changes → data-flow → hypothesis → minimal experiment),
    shotgun fixes forbidden, escalate after 3 failed hypotheses.
  - `onto-build/references/worktree-protocol.md` — the mechanics behind
    `onto set isolation worktree` (creation, env/state copying, cleanup).
  - Enrich `onto-build/references/plan.md` with the writing-plans method (task
    granularity, exact paths, per-task verification).
  - The **brainstorm protocol** reference (clarify → 2–3 approaches →
    trade-offs → user pick, checkpointed) that onto-design walks before
    design.md — comet's "brainstorming cannot be skipped," kept self-contained.
- onto-fix/onto-tweak/onto-build inline sections point at the references instead
  of paraphrasing them; onto-close gains keep/discard options + worktree cleanup
  in its integration step (the finishing-a-development-branch remainder).

**Acceptance.** Same gate asks the same question with the same options in both
tools; every gate answer is inspectable in `onto state --json`; each vendored
protocol is reachable from the phase skill that needs it, and the inline
paraphrases are gone (single source per discipline).

---

## v0.1.11 — Measured gates trio (comet parity, small mechanical wins)

Three items the schema already anticipates; all "shape, not judgment" (B1):

1. **`onto scale <change>`** — derive the verification level from the measured
   `base_ref..HEAD` diff (files/lines; comet-state scale equivalent); prints the
   level and optionally records `verify-scale`.
2. **verify-round discipline** — `onto set verify-result fail` auto-increments
   `observed.verify_rounds` (today nothing writes it); `status`/`doctor` surface
   "N failed rounds" and the ≥3 rule ("user must choose accept-deviation or
   continue") becomes a named finding.
3. **`build_pause`** — a recorded plan-ready pause (`onto set build-pause
   plan-ready|null`) so a stopped session (or model switch) resumes cleanly;
   dispatcher resumes from it.

**Acceptance.** Scale output matches a fixture diff; a third failed verify is a
doctor finding; a paused change resumes at the pause point in a fresh session.

---

## v0.1.12 — Mechanical spec-delta merge

**Problem (top correctness hole).** onto-close's spec merge — RENAMED →
MODIFIED → REMOVED → ADDED application into living specs — is agent-performed
prose; the most destructive step in the workflow depends on model care. Comet
delegates the same step to a CLI.

**Changes.**
- `onto merge-deltas <change>` (also invoked by `onto close` when deltas exist):
  deterministic application of the four sections in order, duplicate-requirement
  and leaked-delta-heading lint post-merge, idempotent via `close.merged`.
- onto-close step 3 shrinks to: assemble plan → confirm → run the command →
  review its report. Skill keeps ADR numbering (rename-scan guard) for now.
- Golden-file tests per section type + conflict cases (MODIFIED targeting a
  RENAMED name; ADDED duplicate = error).

**Acceptance.** A fixture change's deltas merge byte-identically to the golden
output; a doubled run is a no-op; the lint blocks a seeded duplicate.

---

## v0.2.0 — Enforcement layer + CI parity

1. **Hooks projection** — new neutral resource (`[hooks.*]` / framework-shipped)
   rendered per tool: Claude `settings.json` hooks (PreToolUse/Stop) and an
   OpenCode plugin shim reading the same manifest. onto ships phase-guard hooks
   (e.g. Stop → `onto doctor --quiet`) — comet's hook-guard, installed
   declaratively. This is the layer that makes gates non-skippable.
2. **Real-tool E2E in CI** — wire `test/e2e/` (dual-binary matrix driving actual
   Claude Code + OpenCode) into the gate; the parity invariants from v0.1.9
   run on every push, not by hand.
3. **`onto handoff <change> --write`** — hashed, compact context pack per phase
   boundary (comet's handoff package): compaction recovery gets content back,
   not just phase.

**Acceptance.** A denied gate is mechanically intercepted in both tools; CI
fails when either tool stops honoring a rendered invariant.

---

## Deliberately not planned

- Deterministic intent routing (CometIntentFrame) — dispatcher tables are
  simpler and sufficient.
- Artifact language config (en/zh-CN) — no current need.
- Binary self-update — `go install @tag` / release archives own that.
- Per-resource `review_mode` knob — folded into build-mode + reviewer agent.
