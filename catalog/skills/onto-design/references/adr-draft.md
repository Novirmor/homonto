# ADR draft — canonical template

Drafted in the workspace (`<workflow-root>/changes/<name>/adr/<slug>.md`), unnumbered,
`Status: Proposed`. onto-close assigns the next global number and flips to
Accepted. Numbering rules live in `<workflow-root>/adr/README.md`.

## Template

```markdown
# <Title, imperative: "Adopt X", "Use Y for Z">

- **Status:** Proposed
- **Date:** YYYY-MM-DD
- **Change:** <change-name>

## Context

<forces at play; why a decision is needed; alternatives considered>

## Decision

<what we decided, stated actively: "We will …">

## Consequences

<easier/harder; trade-offs accepted; follow-ups>
```

## Rules

- One decision per file; the slug names the decision, not the change.
- Status/Date/Change fields are lint-checked at close — keep the exact
  bullet format.
- If the decision supersedes an existing ADR, say so in Consequences;
  close will mark the old one `Superseded by NNNN`.
