# Architecture Decision Records

`docs/adr/` holds **accepted or superseded** decisions only, one file per
decision: `NNNN-<slug>.md`. It is decision *history* — a record of what was
decided and why, kept even after a decision is superseded.

Because workflow artifacts are not committed
([ADR 0017](0017-stop-committing-workflow-artifacts.md)), an ADR is the durable
record a change leaves behind. When one is owed, how to write it, and the
format: [`../agents/adr.md`](../agents/adr.md).

## Numbering

- Four digits, zero-padded, strictly increasing: `0001`, `0002`, …
- The next number = highest existing number in `docs/adr/` + 1, assigned when
  the ADR is written.
- Numbers are never reused. A superseded ADR keeps its file; its Status becomes
  `Superseded by NNNN`.

## Template

```markdown
# <Title, imperative: "Adopt X", "Use Y for Z">

- **Status:** Proposed | Accepted | Superseded by NNNN
- **Date:** YYYY-MM-DD

## Context / ## Decision ("We will …") / ## Consequences
```

ADRs 0001–0016 also carry a `**Change:**` field naming the OpenSpec change that
produced them. That field is retained as history and is not used for new ADRs.

## Index

- [0024 — Multi-repo support: designated state, cross-repo effect](0024-multi-repo-designated-state-cross-repo-effect.md) (Accepted)
