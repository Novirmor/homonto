# onto-catalog-hygiene — design

Brief implementation description (tweak: no approach comparison needed; the
approach for each item was confirmed with the maintainer during the critique
review).

## D1 — Explorer gains bash (onto + to)

Delete the `bash: false` line from `onto-explorer.md` and `to-explorer.md`
`homonto:` blocks; the rendered Claude denylist drops `Bash` and the OpenCode
permission map drops `bash: deny`. The agents stay `read_only`, `dialogs:
false`, `spawn: []` — the mutation risk bash adds to a read-only agent is
running scripts, which the explorer prompt already scopes to investigation.
Update each file's frontmatter comment and prompt to name the shell-based
grounding tools (code intelligence CLIs, `git log`) as the reason bash exists.

## D2 — Tier vocabulary purge

Replace tier phrasing with model-neutral wording:

- Subagent comments: "fast/cheap trivial-tier model" → "a cheap, fast model
  (the installer picks it per [subagents.<name>.<tool>])"; "review-tier model"
  → "a strong reviewing model"; implementer's "coding" framing likewise.
- Dispatcher delegation table: drop the "Role/model" column entirely — model
  choice is installer config, not framework doctrine; the table keeps Task →
  Dispatch → Capabilities.
- `subagent-protocol.md`: "coding-tier model" / "review-tier model" → neutral.

## D3 — Drop the numeric no-slop self-score

The `no-slop: <artifact> <total>/50` records and the 35 threshold disappear
from: the dispatcher's Prose discipline section, every phase exit checklist
(open, design, verify, close, fix, tweak), and the no-slop skills' own scoring
mandate (onto-no-slop, to-no-slop — the rubric stays as an editing guide; the
self-graded total goes). Replacement contract: run the pass, record
`no-slop: <artifact> done` in notes.md. Rationale: a self-graded score is
decoration; the checklist edit pass is the real control.

## D4 — Misc prose (single edits each)

- `onto.md` (primary agent): body slims to the orchestrator persona + a
  pointer to the dispatcher skill for the four-step loop and delegation table
  (kills the drift-prone duplicate); adds one sentence on step-budget
  exhaustion: state lives in tasks.md/notes.md, a fresh session resumes from
  them.
- `onto-verify/SKILL.md` failure gate: "never *propose* acceptance as the easy
  path" → the gate presents both options with **fix recommended**; acceptance
  is never *recommended*, only presented. Contradiction resolved in favor of
  presenting options honestly.
- `onto-verify` adversarial step: add a scaling sentence — a full verify with
  many scenarios MAY shard the conformance lens across multiple skeptics
  (one per capability); two remains the floor, not the ceiling.
- `onto/SKILL.md` preflight: rtk/graphify warnings are recorded once in the
  change's notes.md (Grounding section); subsequent dispatches of the same
  change skip re-warning. No behavior change to the halt rule for the onto
  binary.

## D5 — Version bumps

`catalog/frameworks/onto/framework.toml` 0.4.1 → 0.5.0, `to` equivalently
(capability profile change = minor, not patch). Catalog `version.txt` is
bumped at the next homonto release, not here (per-change bumps are not the
repo's convention).
