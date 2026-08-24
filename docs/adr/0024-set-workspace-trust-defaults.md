# Set workspace trust defaults

- **Status:** Accepted
- **Date:** 2026-08-24

## Context

The rewritten runtime orchestrates repositories it does not own, records
evidence in a committed control repository, and drives two AI hosts. Each of
those touches user state, so defaults decide how much the tool takes on
faith: a dirty worktree, an unconfirmed scan hit, a secret in a committed
report, or a host integration that reaches beyond its brief each become an
incident the first time they go wrong. The redesign
([`../homonto-workflow-redesign.md`](../homonto-workflow-redesign.md)) fixes
these five defaults; this ADR records them in one place.

## Decision

We will treat the workspace as untrusted by default:

- **Dirty worktrees are rejected, never tidied.** Members with uncommitted
  changes are refused for work creation and integration rather than stashed,
  committed, or merged around. The user resolves their own tree.
- **Scans propose, humans confirm.** Discovery applies standard exclusions
  and classifies candidates, but only the human-confirmed member list is
  persisted; later discovery happens solely through an explicit rescan.
- **Portable evidence is content-free.** Committed checkpoint and archive
  material carries fingerprints and stable facts only — no raw command
  output, report text, tokens, or secrets. Raw evidence stays in local
  ignored state.
- **Claude Code integration is a single skill.** Homonto installs one thin
  command/skill pair per workflow and nothing else — no projection of
  arbitrary skills, commands, or subagents into the host.
- **OpenCode integration is a child-session tool.** Homonto participates in
  OpenCode as a child session driven through a tool boundary, not by writing
  host configuration files.

## Consequences

Refusing dirty trees means legitimate mid-work states (a generated file, an
in-progress edit) block commands until the user commits or discards it —
annoying, but the alternative is the tool inventing commits it cannot
explain. Confirmation-first scanning means discovery is never silent, at the
cost of an explicit init/rescan step on every workspace. Content-free
evidence makes committed records useless for debugging raw failures; logs
must be fetched where they were produced, and cross-machine debugging keeps
that friction. The thin Claude integration leaves users who relied on
projection without it, by design. The child-session OpenCode tool couples us
to that tool surface and will break if OpenCode changes it; the payoff is
that neither host is configured by files Homonto does not own.
