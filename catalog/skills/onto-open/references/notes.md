# notes.md — canonical template (context-loss checkpoint)

The incremental checkpoint for the conversation-shaped phases (open,
design). The derivation table recovers *where* a change is; notes.md
recovers *why*. Created at open; updated before ending ANY turn that
produced new decisions (open and design — the conversation-shaped phases;
build records its plan-ready gate answer here too, and every phase skill
reads it at entry when present); archived with the change.

## Template

```markdown
# Notes: <change-name>

Incremental checkpoint (compaction recovery). Unconfirmed items are
marked *pending*.

## Confirmed

- <fact/decision — with date and, for gate answers, the user's words>

## Pending

- <open question / candidate not yet confirmed>

## Grounding

- <code-intelligence queries run and what they showed; file reads that
  anchor claims>

## Approaches  <!-- design phase -->

- <candidate approaches with one-line trade-offs; mark the CONFIRMED one
  and the date once the gate is answered>
```

## Rules

- Move items from Pending to Confirmed the moment the user answers —
  never leave an answered gate in Pending.
- Never record a decision here that wasn't actually made; notes.md is a
  checkpoint, not a wishlist.
- After compaction: read notes.md FIRST, then re-derive the phase; resume
  from Pending items instead of re-asking Confirmed ones.
