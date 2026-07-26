# Stop committing workflow artifacts; the ADR is the record

- **Status:** Accepted
- **Date:** 2026-07-26

## Context

Comet produced tracked artifacts for every change: proposals, delta specs,
tasks, plans, verification reports, and per-change state under `openspec/` and
`docs/superpowers/` — 77 tracked files describing how changes were produced
rather than what the code does.

They drifted. `AGENTS.md` mandated a `/graphify` skill that was not installed
anywhere, and told agents in one section to use Graphify *only* for broad
analysis while another told them to run `graphify query` *first*. Canonical
specs needed a bespoke gate step, `spec-command-check.sh`, because a spec had
mandated a `homonto agents` command that no longer existed and `openspec
validate` passed it — the validator checks form, not correspondence to reality.

Nothing kept these documents true, and a stale document that reads as
authoritative is worse than no document. Meanwhile the rules that actually
prevented damage — the eof-fixer aborting commits, the `comet` CLI on PATH
lacking its own workflow subcommands, the ban on dogfooding here — were written
down nowhere.

## Decision

We will keep Comet for big development and stop committing anything it
produces.

- `openspec/`, `docs/superpowers/`, and `okf_bundle/` are gitignored. Existing
  tracked copies are deleted; git history retains them.
- The durable record of a change is its code, its tests, and an ADR when a
  decision was made. ADRs are numbered when written, not at archive.
- Smaller work — fixes, mechanical edits, docs — runs directly on a branch with
  no change directory and no phase guards.
- `AGENTS.md` carries only what an agent needs before acting and points at
  `docs/agents/` for detail.
- `spec-command-check.sh` is retired with the specs it read. Its slot in
  `gate.sh` goes to `agents-doc-check.sh`, which asserts that every path,
  script, and reference `AGENTS.md` and `docs/agents/` name actually exists.

## Consequences

- The behavioral contract now lives only in code and tests. There is no
  narrative spec to consult, and no gate step comparing prose to the CLI. Tests
  carry weight they previously shared.
- Change history leaves the working tree. Recovering a past proposal means
  `git show <ref>:<path>` against pre-deletion history.
- Documentation claims are machine-checked for existence but not for
  correctness. `agents-doc-check.sh` would have caught the `/graphify` skill; it
  would not have caught the contradictory Graphify guidance. Judgment-quality
  drift still needs review.
- Comet's archive step becomes local bookkeeping that produces no commit, which
  will read as "nothing happened" to anyone expecting the old archive commit.
- Two lanes mean a judgment call about which one applies, and judgment calls get
  made wrong. The mitigation is stating the lane and switching up when work
  turns out to be bigger, not a rule that removes the judgment.
