# Generate starting configurations in the binary

- **Status:** Accepted
- **Date:** 2026-11-05
- **Partially supersedes:** [0044](0044-the-installer-configures-new-projects.md)
  (where the configuration is generated, and the global session model)

## Context

ADR 0044 had `scripts/install.sh` write the new project's TOML itself:
hard-coded agent lists per engine, a model block for each agent, and the
model copied into `[settings.opencode]`. That logic knew OpenCode only. It
did not know Claude Code, GitLab or Bitbucket, and it kept agent lists in
bash that drift whenever a package adds an agent. Its only check was a
`homonto plan` run afterwards in some tests. Since M3, one
`[routing.default.<harness>]` policy can cover every agent, and the binary
can validate a config before anything is written.

## Decision

- **One declared matrix.** `internal/starter` lists the hosts (GitHub,
  GitLab.com, Bitbucket Cloud), harnesses (OpenCode, Claude Code) and
  builtin engines (onto, to, no) a starting configuration may combine. Every
  combination is supported. A test plans and applies each host × harness
  cell, each engine alone, and everything together.
  `scripts/install-test.sh` takes every host × harness cell from install to
  its first onto change and to task with the real binaries.
- **The binary generates.** `homonto init --harness … --engine … --host …
  --model <harness>=<model>` renders the config, loads it where it will live,
  and writes it only if it is valid. An unsupported value, a missing model or
  an unresolvable repository is refused with nothing written. Without
  selections, `init` writes the commented scaffold, as before.
- **The installer only asks.** It collects the answers and calls `homonto
  init`. No TOML is written from bash.
- **Models are policies, chosen by the user.** Each selected harness gets
  one `[routing.default.<harness>]` model that the user typed. homonto never
  picks one. The installer no longer writes `[settings.opencode] model`, so
  the user's own global OpenCode session model is left alone.
- **No credentials.** The generated file names how each host authenticates
  (`gh`, `glab`, `HOMONTO_BITBUCKET_TOKEN`) and holds no secret or secret
  reference.
- **Evidence per cell.** The matrix declares which evidence each host and
  harness needs. `docs/guides/supported-combinations.md` is generated from it,
  and the release gate checks each cell against the same keys.

## Consequences

Adding a host, harness or engine to `init` is a change to one Go table plus
its tests and its evidence keys. The installer and the docs follow. A
generated config uses a routing policy rather than per-agent pins, so it is
shorter, and a package's new agent needs no installer change. A user who
relied on the installer to set their global OpenCode model now sets it
themselves.

The installer tests now build and run the real binaries. That makes the
suite slower and couples it to the Go toolchain, which the gate already
requires.
