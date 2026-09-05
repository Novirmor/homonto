# The installer offers initialization

- **Status:** Accepted
- **Date:** 2026-09-04

Amends
[`docs/adr/0041-ship-an-interactive-release-installer.md`](0041-ship-an-interactive-release-installer.md),
which said the installer "never runs project commands".

## Context

The first-run gap: the installer left the user with binaries and a printed
next step (`homonto init`) they had to run themselves, and its prompts were
plain reads. New users asked for a guided path to a working configuration,
and gum (charmbracelet/gum) gives shell scripts a real selection UI without
new trust boundaries — but only when it is already installed.

## Decision

After installing, `scripts/install.sh` asks — via gum when it is on `PATH`
and stdin is a TTY, plain reads otherwise (HOMONTO_UI forces either) —
whether to run `homonto init` in the current directory, and runs it only on
explicit confirmation. `homonto init` never overwrites, so accepting in an
already-configured directory is safe. The default workflow-binary choice
becomes `both` (onto and to are complementary, ADR 0042). gum is never
downloaded by the installer; using it stays the user's decision.

## Consequences

One confirmed run takes a machine from nothing to a scaffolded
`homonto.toml`. The installer now executes a project command on the user's
behalf — confined to the non-destructive `homonto init`, gated on a
confirmation that defaults to no. The plain-prompt path remains the contract
for scripted use and for the mocked tests.