# Homonto

Homonto governs AI-assisted work in a repository. It records the workflow
state, issues scoped assignments, runs configured checks, and keeps the record
needed to resume or review the work later.

Use it when an agent needs to make a change but a human still owns the scope,
decisions, and final integration.

## Build from source

The workflow product has not had a tagged release. Tags through `v0.11.0`
contain the retired configuration projector, so build this branch instead of
using `go install ...@latest`.

```bash
git clone https://github.com/noviopenworks/homonto
cd homonto
go build -o homonto .
./homonto init --workflow task
./homonto host install --tool claude
./homonto task start fix-login --goal "Login fails after a restart."
```

The installed host integration asks Homonto for the next action with
`next --json`. Homonto accepts reports only for the action and workspace state
it issued.

## What Homonto Controls

- Verification commands run under the configuration Homonto records.
- Assignments receive a write scope. Homonto checks the resulting diff before
  it accepts a report.
- Task uses `plan -> do -> done`. Change uses `open -> design -> build ->
  verify -> close`, with Fix and Tweak paths for smaller work.
- `handoff` creates a portable checkpoint. `attach` resumes it from another
  machine or clone.

## Limits

- The write boundary is a cooperating-host process gate plus final diff
  validation. It is not an operating-system sandbox.
- Homonto prepares integration branches or staged directories. It does not
  merge them into a member's branch.
- No exposed command fetches from the network. The self-update mechanism is
  not available to users yet.

## Documentation

Start with the [documentation home](docs/README.md). It separates a first Task
tutorial, operator procedures, workflow concepts, command and protocol
reference, and the release procedure.

## Status

Pre-release. The command surface, protocol, and storage schema may change
between commits.
