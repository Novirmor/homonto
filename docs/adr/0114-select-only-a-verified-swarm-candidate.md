# Select only a verified swarm candidate

- **Status:** Accepted
- **Date:** 2026-11-06

## Context

A swarm compares candidates to integrate one of them. The comparison is only
fair if every candidate faces the same checks on the commits being compared,
and "deliver the verified winner" needs a definition of verified. Cleanup
must remove the alternatives without losing work the user never saw.

## Decision

- `swarm check` runs every configured integration check matching the swarm's
  repositories against each candidate's task, one candidate at a time, so
  candidates never compete for ports or caches. A failing candidate is a
  comparison result; only interruption fails the command.
- `swarm compare` reads recorded evidence and runs nothing. It reports per
  candidate the routed models, revisions, commits and diff size against the
  pinned base, each check's status and staleness, and previews, as a text
  table, JSON, or a local HTML page with the running previews side by side.
- A candidate is verified when each changed repository has committed work
  beyond the base, nothing uncommitted, and every matching check passed on the
  current commits. With no matching check, selection needs
  `--fallback-verified`, the caller's attestation; the flag is refused when a
  configured check exists.
- `swarm select` records the choice and every candidate's revisions at that
  moment. The user chooses; the skill asks through the native dialog.
- The winner is delivered through the normal workflow. Cleanup stops previews
  first, then removes rejected candidates' worktrees and deletes a rejected
  branch only while it still points at the recorded revision (`update-ref -d`
  with the old value). The winner is released as workspace-task cleanup
  releases any task. `--keep-rejected` keeps rejected branches; `--abandon`
  releases everything without a winner and keeps every branch.

## Consequences

The same verification gate as workspace tasks decides; no swarm-specific
scoring exists, and ranking candidates stays the user's call. Checks run
serially, so a swarm's check takes up to three times one task's. A rejected
branch that moved after selection survives cleanup and must be deleted by
hand.
