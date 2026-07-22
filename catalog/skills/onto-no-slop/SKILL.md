---
name: onto-no-slop
description: Remove AI writing patterns from prose. Use when drafting, editing, or reviewing any onto prose artifact — proposals, designs, ADRs, guides, verification reports, commit messages — to eliminate predictable AI tells.
metadata:
  trigger: Writing prose, editing drafts, reviewing content for AI patterns
  author: Hardik Pandya (https://hvpandya.com)
  source: adapted from stop-slop, https://github.com/hardikpandya/stop-slop (MIT); vendored into the onto framework as onto-no-slop
---

# onto-no-slop

Eliminate predictable AI writing patterns from prose. This is the onto
framework's build of the **stop-slop** skill by Hardik Pandya, unchanged in its
rules and extended with a short section on applying them inside onto.

## Core Rules

1. **Cut filler phrases.** Remove throat-clearing openers, emphasis crutches, and all adverbs. See [references/phrases.md](references/phrases.md).

2. **Break formulaic structures.** Avoid binary contrasts, negative listings, dramatic fragmentation, rhetorical setups, false agency. See [references/structures.md](references/structures.md).

3. **Use active voice.** Every sentence needs a human subject doing something. No passive constructions. No inanimate objects performing human actions ("the complaint becomes a fix").

4. **Be specific.** No vague declaratives ("The reasons are structural"). Name the specific thing. No lazy extremes ("every," "always," "never") doing vague work.

5. **Put the reader in the room.** No narrator-from-a-distance voice. "You" beats "People." Specifics beat abstractions.

6. **Vary rhythm.** Mix sentence lengths. Two items beat three. End paragraphs differently. No em dashes.

7. **Trust readers.** State facts directly. Skip softening, justification, hand-holding.

8. **Cut quotables.** If it sounds like a pull-quote, rewrite it.

## Quick Checks

Before delivering prose:

- Any adverbs? Kill them.
- Any passive voice? Find the actor, make them the subject.
- Inanimate thing doing a human verb ("the decision emerges")? Name the person.
- Sentence starts with a Wh- word? Restructure it.
- Any "here's what/this/that" throat-clearing? Cut to the point.
- Any "not X, it's Y" contrasts? State Y directly.
- Three consecutive sentences match length? Break one.
- Paragraph ends with punchy one-liner? Vary it.
- Em-dash anywhere? Remove it.
- Vague declarative ("The implications are significant")? Name the specific implication.
- Narrator-from-a-distance ("Nobody designed this")? Put the reader in the scene.
- Meta-joiners ("The rest of this essay...")? Delete. Let the essay move.

## Scoring

Walk each dimension as an edit question — if the honest answer is the bad
half, revise that aspect before the gate:

| Dimension | Question |
|-----------|----------|
| Directness | Statements or announcements? |
| Rhythm | Varied or metronomic? |
| Trust | Respects reader intelligence? |
| Authenticity | Sounds human? |
| Density | Anything cuttable? |

There is no numeric self-score: a model grading its own prose against a
threshold always passes itself. The questions are the control; answer them
honestly and edit.

## Examples

See [references/examples.md](references/examples.md) for before/after transformations.

## Using this inside onto

onto phases write prose the reader keeps: `proposal.md`, `design.md`,
`notes.md`, ADR drafts, `verification.md`, guide updates, and commit messages.
Run these rules over every such artifact before its phase gate, so the record a
human reads later sounds like a human wrote it.

This pass edits the **prose you wrote** — the paragraphs a human reads. It
never edits structure another skill or a machine depends on. Off-limits,
always:

- **Machine-read markers.** `Status: Confirmed`, `Result: pass`,
  `Preset: fix`, `Depends-on:`, checkbox syntax `- [ ]`/`- [x]`, the
  `SHALL`/`MUST` first line of a requirement, and a scenario's
  `GIVEN`/`WHEN`/`THEN` bullets. Phase derivation and the close lint grep
  these verbatim — reword one and you break the workflow.
- **A requirement's normative wording.** Never soften, re-voice, or
  "de-slop" the text of a spec requirement or an accepted ADR decision.
  Keep exact terms: a value that MUST be rejected needs "MUST"; keep
  "atomically," "idempotently," "never persists" where they carry meaning.
  Cut the empty adverb ("really," "simply"), not the load-bearing one.
- **Mandated structure.** Template section headings, required list items
  (the open-lite five-part summary, GIVEN/WHEN/THEN), and a template's own
  punctuation are format, not prose. Don't drop a required item to "make
  two beat three," and don't strip a template's structural em dash.

Within those bounds everything applies: active voice, name the actor, be
specific, cut the throat-clearing, vary the rhythm, drop the *manufactured*
"not X, it's Y" reversal (keep a genuine distinction like "files win
downward; gates win upward"), no em dashes in your own sentences. When in
doubt whether a line is prose or contract, leave it.

## License

MIT. Original skill by Hardik Pandya (https://github.com/hardikpandya/stop-slop).
