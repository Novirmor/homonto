# homonto

**Governed AI coding workflows with binary-owned state.**

Homonto runs a development workflow that an agent cannot skip steps in. It
issues typed assignments to subagents, executes the verification commands
itself rather than believing a claim that they passed, gates advancement on
what actually changed on disk, and leaves one commit-ready record of the
work — ordinary completion archives it; a handoff commits it.

## Install

```bash
git clone https://github.com/noviopenworks/homonto
cd homonto
go build -o homonto .
```

Every published tag, through `v0.11.0`, is the legacy configuration
projector the rewrite replaced — `go install
github.com/noviopenworks/homonto@latest` builds that old product, not this
one. Build from source until the first workflow release is tagged.

```
homonto init --workflow task
homonto host install
homonto task start fix-login --goal "Login fails after a restart."
```

From there the host agent runs `homonto next --json` and does exactly what
comes back.

## What it is for

An agent asked to "fix the login bug" will do the work, tell you the tests
pass, and be right most of the time. Homonto is for the rest of the time —
and for work whose record has to survive the session it was done in.

- **Homonto runs the checks.** Not the agent, and not on the agent's word.
  Commands run as argument vectors, from configured directories, with
  bounded timeouts and an environment allowlist, and the evidence records
  what actually ran.
- **Writes have a boundary that does not depend on cooperation.** A write
  hook blocks what a host presents for approval, and — because a shell
  command can walk straight past that — Homonto independently validates the
  resulting diff before accepting any report.
- **The recorded step is never trusted alone.** Every step rests on a
  baseline of fingerprints. When one moves, the workflow returns to the
  earliest step it affects rather than trusting where it thinks it is.
- **Humans decide the consequential things.** Path classification, scope
  and design approval, accepting a blocking finding, what to do after three
  failed repairs. Homonto pauses and explains; it never picks.
- **Work is portable.** `homonto handoff` commits a transferable checkpoint;
  `homonto attach` picks it up on another machine. One hop only: an attached
  work is consumed and cannot be handed off again — moving it once more is a
  forced takeover, not a handoff.

## The two workflows

A workspace runs one or the other, chosen at `init`.

**Task** — `plan → do → done`. For work that needs doing carefully but does
not need a paper trail of decisions. Leaves one checked-off record.

**Change** — `open → design → build → verify → close`, with **Fix** and
**Tweak** presets for smaller work. Starting one opens a local
classification candidate first: read-only explorers and a skeptic assess the
request, Homonto suggests a path and explains why, and nothing is written
until a human confirms. A preset that outgrows itself pauses and asks;
it never upgrades itself.

Both use all four subagent roles — explorer, implementer, reviewer, skeptic
— and both run implementers in parallel in isolated worktrees or non-Git
snapshots, integrated by a dedicated integration assignment.

## Host integration

Claude Code and OpenCode, on Linux and macOS. Installation is thin by
design: one command, one skill, one read-only resume probe, one write hook.
The generated files contain no workflow transitions, no required-artifact
rules, no routing policy, and no subagent prompts — every one of those lives
in the binary, where it is versioned and tested and a host cannot disagree
with it.

Generated files are project-local and gitignored by default. Committing
them is an explicit opt-in.

## What it does not do

- **No network.** No command in the current binary touches the network: the
  signed-manifest fetch the update design calls for is implemented in the
  binary but not yet exposed as a command, and nothing checks for updates on
  its own. Homonto never calls a model provider; your host tool does that.
- **No sandbox.** The write hook is a process gate for a cooperating host.
  Homonto does not claim to prevent an out-of-scope write; it refuses to
  build on one.
- **No merging.** Integration branches and staged directories are left ready
  for you to handle. Homonto never merges into a member's own branch.

## Documentation

| | |
|---|---|
| [Getting started](docs/guides/getting-started.md) | Install, initialize, run one task end to end |
| [Configuration](docs/guides/configuration.md) | The workspace manifest |
| [Task workflow](docs/guides/task-workflow.md) | `plan → do → done` |
| [Change workflow](docs/guides/change-workflow.md) | Full, Fix, Tweak, and upgrades |
| [Protocol](docs/guides/protocol.md) | What a host speaks |
| [Host integration](docs/guides/host-integration.md) | What gets installed and why it is thin |
| [Security](docs/guides/security.md) | The write boundary, redaction, trust |
| [Recovery](docs/guides/recovery.md) | Crashes, handoff, attach, takeover |
| [Updates](docs/guides/updates.md) | Signed releases and rollback |
| [CLI reference](docs/guides/cli-reference.md) | Every command |
| [Troubleshooting](docs/guides/troubleshooting.md) | When something refuses |

Design decisions live in [`docs/adr/`](docs/adr/README.md). The product
this repository shipped before the rewrite is described by ADRs marked
superseded by [0023](docs/adr/0023-rebuild-homonto-as-workflow-orchestrator.md).

## Status

Pre-release. The command surface, the protocol, and the storage schema are
still moving, and there is no compatibility promise between commits.
