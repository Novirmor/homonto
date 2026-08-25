# Release notes intro

This file is prepended to every GitHub release's auto-generated notes by
the `release` workflow (`--notes-file docs/release-notes.md
--generate-notes`), so every release states its limitations up front. Keep
it short; the per-release changelog is generated below it.

---

## What this is

Homonto runs governed AI-coding workflows. You start a Task or a Change, it
issues scoped assignments to your coding agent, holds the state, enforces
what each assignment may write, runs your checks, and leaves a record that
someone who was not there can pick up.

One binary, `homonto`, for Linux and macOS on amd64 and arm64. It has no
daemon, no services, and no network access — no command in the current
binary touches the network at all.

Requires [Claude Code](https://claude.com/claude-code) or
[OpenCode](https://opencode.ai). `homonto host install` writes thin
wrappers into the host's own configuration; every decision stays in the
binary.

## Accepted limitations

- **Linux and macOS only.** There is no Windows build. The filesystem layer
  is built on fd-anchored, symlink-refusing syscalls, and no equivalent has
  been written or tested for Windows.
- **Two hosts.** Claude Code and OpenCode. Other agents can drive the
  protocol directly (`homonto next --json`, `homonto report`), but nothing
  else ships an integration.
- **Git integration expects a clean worktree.** An assignment cannot be cut
  from a member with uncommitted changes; Homonto refuses rather than
  deciding what to do with your work.
- **Integration conflicts are yours.** Homonto creates the integration
  worktree and applies each assignment's commits; a conflict is left in
  place for a human to resolve.
- **Checks run in an allowlisted environment.** Nothing ambient is
  inherited — a check that needs `PATH` has to say so. This is stricter
  than most tools, deliberately: a check that silently depends on your
  shell is a check whose evidence does not describe what ran.
- **A locally built binary cannot update itself.** It carries no signing
  root, so it verifies nothing and refuses. `homonto update trust` says so.
- **The self-update command is not wired yet.** The fetch, verify, stage,
  and activate mechanism is implemented in the binary and tested, but no
  command exposes it, and no published build carries a signing root. Until
  one does, upgrading means building from source; recovery of an
  interrupted activation is the one half that is wired into startup.
- **The write boundary is not a sandbox.** It stops an assignment from
  writing outside its scope through both a cooperating host gate and an
  independent final diff. It does not contain a process that is actively
  trying to escape.

## Upgrading

Build from the tag you want. The signed self-update flow — manifest
verified against compiled-in roots, candidate interrogated by running it,
activation journaled and rolled back exactly on failure — is implemented in
the binary but not yet exposed as a command, so these guarantees describe
the mechanism, not something you can run today.
