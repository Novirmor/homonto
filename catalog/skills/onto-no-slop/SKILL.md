---
name: onto-no-slop
description: Edit onto proposals, designs, ADRs, guides and reports for clarity while preserving technical meaning, evidence and machine-read structure.
metadata:
  author: Hardik Pandya (https://hvpandya.com)
  source: adapted from stop-slop, https://github.com/hardikpandya/stop-slop (MIT); revised for technical workflow prose
---

# onto-no-slop

## Purpose and entry

Edit prose being written or reviewed within the authorized task. Follow the shared
[workspace policy](../homonto/references/workspace-policy.md) before file edits.
This is a lightweight edit pass, not another gate or a reason to create notes.

## Actions

1. Preserve accuracy: qualifiers, uncertainty, constraints and genuine distinctions.
2. State the useful point early. Remove repeated explanations, empty emphasis and
   announcements that add no information.
3. Name the relevant actor or component. Prefer active voice when it clarifies
   responsibility; passive voice and nonhuman subjects are valid when accurate.
4. Replace vague claims with concrete behavior or evidence. Never strengthen a
   claim merely to make it shorter.
5. Use the structure the reader needs. Keep useful lists, technical adverbs and
   punctuation; there are no quotas for sentence length, alternatives or list size.

## Completion evidence

Read the edited passage against the original: same meaning, less friction. No
numeric score or `no-slop: done` receipt is required. Continue the owning phase.

Never alter machine-read markers (`Status:`, `Result:`, `Preset:`, `Depends-on:`),
checkboxes/task IDs, required headings, normative requirements, accepted ADR
decisions, GIVEN/WHEN/THEN scenarios, literal commands or captured output. Preserve
load-bearing terms such as “atomically” and “idempotently.” When uncertain, leave
the line and resolve its meaning before editing. Existing style receipts are history.

For examples, read [technical edits](references/examples.md). The optional
[phrase](references/phrases.md) and [structure](references/structures.md) references
are editing aids, not word bans.

## License

MIT. Adapted from Hardik Pandya's stop-slop; the technical editing rules above
replace the original blanket stylistic bans.
