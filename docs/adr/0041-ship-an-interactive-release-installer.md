# Ship an interactive release installer

- **Status:** Accepted
- **Date:** 2026-09-04

## Context

`go install` is the primary install path but requires a Go toolchain, and the
release archives need manual `SHA256SUMS` verification plus PATH plumbing.
New Linux/macOS users need a guided path. Any scripted install must not weaken
the checksum boundary, modify the user's shell configuration, or run project
commands.

## Decision

We will ship `scripts/install.sh`, a Bash installer for Linux and macOS
(amd64/arm64) that asks which binaries to install and where, resolves the
latest release through the GitHub API (or accepts an explicit version),
downloads the archives, verifies each digest against the release `SHA256SUMS`,
extracts only the expected executable, and installs it. It never edits shell
configuration — the PATH export is printed for the user to apply — and never
runs project commands. Documentation recommends download-then-run
(`curl -o install.sh` + `bash install.sh`), never `curl | bash`. Windows
installations stay manual ZIP extraction. The installer is pinned by
mocked-network tests (`scripts/install-test.sh`) that run in the gate.

## Consequences

Users without a Go toolchain get a guided, verified install whose trust anchor
is the GitHub release's `SHA256SUMS`; the user keeps control of PATH and of
what runs. The script is a second install surface to maintain and test, and
Windows users still follow the manual path.