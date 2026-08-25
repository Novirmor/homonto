# Adopt a configurable skill install scope (user vs project)

- **Status:** Superseded by 0023
  `docs/specs/config-model.md` "Explicit resource declarations" and
  `docs/superpowers/specs/2026-07-09-dual-binary-release-design.md`). Each
  `[skills.<name>]` resource now carries its own required `scope`; the
  list-style `[skills] scope` default and `[skills] own` list no longer exist.
- **Date:** 2026-07-06
- **Change:** per-project-skills

## Context

homonto installs every owned skill into a single global location
(`~/.claude/skills/<name>`, `~/.config/opencode/skills/<name>`) because the install
root is hardcoded to `os.UserHomeDir()` and threaded unchanged through `engine.Build`
into both adapters. There is no way to keep a project's skills with that project, which
users want for in-repo, versioned skill sets that don't leak into their global tool config.

Forces:
- **OpenCode's project and global subpaths differ** — project skills live under
  `<repo>/.opencode/skills/` while global skills live under `~/.config/opencode/skills/`
  (per OpenCode's documented search order). Claude, by contrast, uses `.claude/skills/`
  in both scopes. So the destination is *not* a simple base-directory swap; the mapping
  depends on both tool and scope.
- The skill path is currently joined inline at three sites per adapter (`links`,
  `ObserveHashes`, `Apply`'s delete branch) plus two sites in `doctor`. Duplicating a
  scope-conditional across five places invites drift bugs.
- State records each link under the key `skill.<name>`, which is **location-independent**.
  Changing scope changes the destination but not the key, so orphan pruning (which only
  deletes *de-declared* skills) would leave the old-location link dangling.

Alternatives considered: a single `skillsBase` base-dir swap (rejected — wrong for OpenCode);
a per-invocation `--local` CLI flag (rejected — the user wants it declared in config, persisted);
per-skill scope (rejected — out of scope, larger schema); the engine computing each adapter's
full skills dir (rejected — leaks per-tool path knowledge out of the adapter).

## Decision

We will add `[skills] scope = "user" | "project"` to `homonto.toml` (empty/absent = `user`,
back-compat), governing **skill symlinks only** — MCP servers and settings always project into
the global tool files.

We will introduce `internal/skillpath.Dir(tool, scope, home, projectRoot)` as the single source
of truth mapping `(tool, scope)` to a skills directory (claude→`.claude/skills`, opencode→
`.config/opencode/skills` for user and `.opencode/skills` for project, rooted at `home` or
`projectRoot`). Both adapters and `doctor` call it; `engine.Build` resolves
`projectRoot = dir(homonto.toml)` and threads `scope`+`projectRoot` into the adapters.

We will make a scope switch an explicit **relocate**: `plan` renders the move (old→new location)
and `apply` prunes the inactive-scope link via `link.Remove` (a conflict-safe no-op when absent),
so no orphan remains.

## Consequences

- Easier: project-scoped skills; one authoritative path function; scope-aware
  status/doctor/drift come almost for free (drift already flows through `ObserveHashes`).
- Trade-off: the adapter constructor signatures change (`New(home, content, scope, projectRoot)`)
  and `Engine` gains a `ProjectRoot` field; a modest ripple through existing adapter tests.
- Follow-up: OpenCode also reads `.claude/skills/` in a project as a compatibility source, so a
  Claude project-scope install is *also* visible to OpenCode; we still create the native
  `.opencode/skills/` link for clarity and symmetry. A future change could deduplicate.
- No new secret, state, or apply-pipeline semantics; the JSONC-comment and secret-safety
  invariants are unaffected.
