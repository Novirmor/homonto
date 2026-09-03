# notes.md — canonical template (context-loss checkpoint)

The incremental checkpoint for the conversation-shaped phases (open,
design). The derivation table recovers *where* a change is; notes.md
recovers *why*. Created at open; updated before ending ANY turn that
produced new decisions (open and design — the conversation-shaped phases;
build records its selected configuration here too, and every phase skill
reads it at entry when present); archived with the change.

## Template

```markdown
# Notes: <change-name>

Incremental checkpoint (compaction recovery). Unconfirmed items are
marked *pending*.

## Confirmed

- <fact/decision — with date, basis, and the user's words when they supplied it>

## Pending

- <open question / candidate not yet confirmed>

## Approaches  <!-- design phase -->

- <candidate approaches with one-line trade-offs; mark the SELECTED one,
  its basis, and the date>
```

## Rules

- **Grounding does not live here.** What was queried and read belongs to the
  artifact that rests on it: open-phase grounding (including the preflight's
  tooling record) in `proposal.md` `## Grounding`, design-phase grounding in
  `design.md` `## Grounding`. Those two are checked at close; a third copy
  here was checked by nothing and read by no one.
- Move items from Pending to Confirmed when repository evidence or the user
  settles them. Never leave a resolved decision in Pending.
- Never record a decision here that wasn't actually made; notes.md is a
  checkpoint, not a wishlist.
- After compaction: read notes.md FIRST, then re-derive the phase; resume
  from Pending items instead of re-asking Confirmed ones.
