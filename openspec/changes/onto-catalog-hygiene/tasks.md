# onto-catalog-hygiene — tasks

## 1. Explorer bash (D1)

- [x] 1.1 `catalog/subagents/onto-explorer.md`: drop `bash: false`, rewrite
  the frontmatter comment and prompt's method section to name shell grounding
  tools (code-intelligence CLIs, `git log`) as in-scope.
- [x] 1.2 `catalog/subagents/to-explorer.md`: same change.
- [x] 1.3 Confirm agentfm render: no `Bash` in the Claude denylist, no
  `bash: deny` in the OpenCode permission map for both explorers (unit-render
  via existing agentfm tests or a scratch render).

## 2. Tier purge (D2)

- [x] 2.1 Subagent frontmatter comments: `onto-explorer.md`,
  `onto-reviewer.md`, `onto-skeptic.md`, `onto-implementer.md`,
  `to-explorer.md`, `to-reviewer.md`, `to-skeptic.md` — model-neutral wording.
- [x] 2.2 `catalog/skills/onto/SKILL.md` delegation table: remove the
  "Role/model" column.
- [x] 2.3 `catalog/skills/onto-build/references/subagent-protocol.md`:
  neutral wording for implementer/reviewer model references.
- [x] 2.4 Grep gate: no `tier` / `trivial-tier` / `review-tier` /
  `coding-tier` in `catalog/` outside legitimate uses (to-do's "trivial ones"
  prose is unrelated and stays).

## 3. no-slop score removal (D3)

- [ ] 3.1 `catalog/skills/onto/SKILL.md` Prose discipline: drop the
  `<total>/50` record + threshold; new contract `no-slop: <artifact> done`.
- [ ] 3.2 Exit checklists: onto-open, onto-design, onto-verify, onto-close,
  onto-fix, onto-tweak — same replacement.
- [ ] 3.3 `onto-no-slop/SKILL.md` + `to-no-slop/SKILL.md`: scoring mandate
  removed, rubric retained as editing guidance; `to/SKILL.md` + b
  `to-plan/SKILL.md` score references updated.
- [ ] 3.4 Grep gate: no `/50` or `no-slop:.*<total>` left in `catalog/`.

## 4. Misc prose (D4)

- [ ] 4.1 `catalog/subagents/onto.md`: slim body to persona + dispatcher
  pointer; add step-exhaustion recovery sentence.
- [ ] 4.2 `catalog/skills/onto-verify/SKILL.md`: failure-gate wording (present
  both options, recommend fix, never recommend acceptance); skeptic scaling
  sentence in step 2b.
- [ ] 4.3 `catalog/skills/onto/SKILL.md` preflight: rtk/graphify warn once per
  change (recorded in notes.md Grounding), skip re-warning on later
  dispatches.

## 5. Versions + verification (D5)

- [ ] 5.1 `catalog/frameworks/onto/framework.toml` version 0.4.1 → 0.5.0;
  `catalog/frameworks/to/framework.toml` 0.3.1 → 0.4.0.
- [ ] 5.2 `go build ./... && go test ./internal/catalog/... ./internal/agentfm/...`
  green (catalog content is embedded; render tests must still pass).
- [ ] 5.3 Full `go test ./...` green.
