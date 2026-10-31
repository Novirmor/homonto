# Provision dev tools with mise

- **Status:** Accepted
- **Date:** 2026-10-31

## Context

`task check` and the gate call golangci-lint, task, Node 24, and pre-commit,
but only Go was pinned in a way a fresh machine could act on. `task tools`
installs the linter into `GOBIN`, which is often not on `PATH`, so `task check`
failed with "executable not found" on a machine where the linter was installed.
Each contributor assembled the rest by hand, at whatever version they had.

## Decision

We will declare every developer tool in a root `mise.toml`, so
`mise trust && mise install` provisions them all at pinned versions on `PATH`.
mise only supplies tools: `Taskfile.yml` stays the one entry point for what to
run, and no task requires mise. The authoritative pins stay where they were:
go.mod's `toolchain` line for Go and `GOLANGCI_LINT_VERSION` in
`scripts/gate.sh` for the linter. `scripts/tool-pins-check.sh` fails `task
check` and the gate when `mise.toml` or a workflow's `setup-go` copy drifts
from them. CI keeps `setup-go`/`setup-node`.

## Consequences

- One command provisions a working dev machine, and tool versions match
  across contributors.
- Go and golangci-lint pins now have one more copy to bump; the pin check makes
  a missed copy a failing check instead of a silent difference.
- task, Node, and pre-commit are pinned only in `mise.toml`; contributors
  without mise still bring their own, as before.
- This reverses the earlier preference for Taskfile-only tooling, but only for
  provisioning: no mise tasks, no mise-only workflow.
