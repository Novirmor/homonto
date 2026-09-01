# Getting started

A hands-on walkthrough of `homonto` and `onto`. `homonto` projects your
`homonto.toml` into OpenCode, Terraform-style: `plan`, then `apply`. OpenCode
is the only adapter; Claude Code and codex support was removed in v0.13.0
(configs naming them fail at load naming the key). `onto` gates a change
through `open → design → build → verify → close`. onto's mutating commands
need the onto framework installed *by* homonto first.

> A third binary, `to`, is the lightweight alternative to onto (`plan → do →
> done`, no gates). See the [to workflow guide](to-workflow.md) and the
> [to reference](to-reference.md). onto and `to` are an exclusive choice per
> repository; this walkthrough uses onto.

> Output goes to **stderr**. Redirect with `2>&1` when scripting.

## 1. Install

```bash
go install github.com/noviopenworks/homonto@latest           # homonto
go install github.com/noviopenworks/homonto/cmd/onto@latest  # onto
```

Or grab the prebuilt binaries and `SHA256SUMS` from the GitHub release
(Linux/macOS/Windows, amd64/arm64). From a checked-out repo use
`go install .`, not a bare `go build .`: the output name collides with the
`homonto/` content directory (see [troubleshooting](troubleshooting.md)).

Verify:

```console
$ homonto version
homonto v0.5.0
$ onto version
onto v0.5.0
```

## 2. homonto in five commands

```console
$ homonto init            # scaffold homonto.toml + .gitignore + .env.example (never overwrites)
$ $EDITOR homonto.toml    # declare MCPs / skills / plugins / settings
$ homonto plan            # show the diff — writes nothing, resolves no secrets
$ homonto apply           # confirm [y/N] (--yes to skip), then write atomically
$ homonto status          # report drift / pending / clean
```

A realistic first `homonto.toml`:

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]      # every tool by default (OpenCode)

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env = { BRAVE_API_KEY = "${BRAVE_API_KEY}" }    # a reference, never a literal secret
targets = ["opencode"]                          # the only valid target

[skills.my-notes]
source = "local:my-notes"                       # → homonto/skills/my-notes/
scope = "project"                               # required: user | project

[settings.opencode]
model = "anthropic/claude-opus-4-8"
model_variant = "thinking"                        # projects as ...opus-4-8#thinking
```

**Model variants.** Set `model_variant` beside `model` to select a provider's
variant tier, such as `thinking`, `high`, `xhigh`, or `max`. homonto projects
the pair as OpenCode's single `provider/model#variant` identifier. Alternatively
write that suffix directly in `model`, but never both forms: `model_variant`
without `model`, or a variant combined with an already-suffixed model, fails at
load. Subagents use the same `variant = "…"` field inside their
`[subagents.<name>.opencode]` block. See the
[configuration reference](configuration.md#settings--settingsopencode) for
the full validation rules.

`plan` prints a Terraform-style diff (`+` create, `~` update, `-` delete) and
leaves secrets as unresolved tokens:

```console
$ homonto plan
opencode:
  + mcp.brave = {"command":["npx","-y","@modelcontextprotocol/server-brave-search"],"env":{"BRAVE_API_KEY":"${BRAVE_API_KEY}"},"enabled":true,"type":"local"}
  + mcp.codegraph = {"command":["codegraph","serve","--mcp"],"enabled":true,"type":"local"}
  + setting.model = "anthropic/claude-opus-4-8"
  + skill.my-notes = …/.opencode/skills/my-notes -> …/homonto/skills/my-notes
```

`apply` resolves every secret first and aborts before any write if one fails,
then writes surgically, keeping every key it does not manage. `status` tells
the three states apart:

```console
$ homonto status
1 config change(s) awaiting apply (run `homonto apply`)   # you edited the toml
opencode setting.model drifted (will reset on apply)      # disk changed outside homonto
No drift.                                                 # everything matches
```

**Secrets** are referenced, never stored: `${pass:path}` (via
[`pass`](https://www.passwordstore.org/)) or `${ENV_VAR}`.
`.homonto/state.json` holds only the token plus a sha256 hash, so it is safe
to share. See [secrets](secrets.md).

**Health check:** `homonto doctor` verifies `pass` is on `PATH`, the tool
config locations exist, and each owned skill has intact content and its tool
links.

## 3. Your first owned skill

Skills you author live under `homonto/skills/` next to `homonto.toml` and are
**symlinked** into each tool, so editing the source is instantly live
everywhere:

```console
$ mkdir -p homonto/skills/my-notes
$ printf -- '---\nname: my-notes\ndescription: My note conventions\n---\n' > homonto/skills/my-notes/SKILL.md
$ homonto apply --yes
```

Each skill declares its own `scope` (required, no default): `user` links into
`~/.config/opencode/skills/`; `project` links into the repo itself
(`.opencode/skills/`). Switching scope is clean: `plan` shows the link
relocating, and `apply` removes the old link as it creates the new one.

## 4. The onto workflow

Install the framework via homonto, then apply. Every subagent the framework
expands must declare **an explicit model**:

```toml
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
```

```console
$ homonto apply --yes            # materializes the onto-* skills, commands, subagents

$ onto init && onto new add-search
$ onto advance add-search        # open → design
$ onto advance add-search        # error: cannot leave "design": missing design.md
$ printf '# Design\n' > docs/changes/add-search/design.md
$ onto set isolation add-search branch
$ onto advance add-search        # design → build
```

Each transition needs that phase's deliverables. They accumulate; this table
is the `full` workflow, and the `fix`/`tweak` presets run a reduced path:

| Leaving | Requires |
|---|---|
| `open` | `proposal.md` |
| `design` | + `design.md`, `tasks.md`, `isolation` set |
| `build` | + `plan.md` **and every `tasks.md` box checked** |
| `verify` | + `verification.md`, `verify-result = pass` |

`verify → close` also blocks on uncommitted work: this change's own artifacts
or any source path, but not another change's in-flight docs (`onto dirt
<change>` classifies each path, and the refusal names what blocks). Close has
its own evidence gates:

```console
$ onto close add-search          # error: change not merged (close.merged=false)
$ onto set close-merged add-search && onto set guides add-search updated
$ git add -A && git commit -q -m "close evidence"
$ onto close add-search          # archived to docs/changes/archive/2026-07-14-add-search
```

`close` also refuses while any dependency is unresolved (see `onto graph`).
Terminal states: archived via `onto close` (success) and `onto abandon`
(failure). Read-only inspectors: `onto status`, `doctor`, `state --json`,
`gate --json`, `scale`, `graph`, `handoff`, `dirt`.

Concepts and the skills side: [the onto workflow](onto-workflow.md). Every
command and gate: [onto reference](onto-reference.md).

## 5. Supported / not supported

| Supported | Notes |
|---|---|
| MCP servers, settings, skills, plugins, TUI settings | OpenCode, full — the only adapter (Claude Code and codex were removed in v0.13.0) |
| Frameworks (`[frameworks.*]`) | builtin `onto` or `to` (mutually exclusive); also `local:` roots and digest-pinned `remote:` sources |
| Commands, subagents (`builtin:` / `local:`) | subagents: `mode = link` (default) or `copy` |
| Remote sources (`remote:…`) | subagents and frameworks; **require `digest = "sha256:…"`**; fetched, verified, pinned, cached |

| Not supported (accepted for beta) | Detail |
|---|---|
| OpenCode JSONC comments | any apply that writes `opencode.jsonc` drops comments (no-op applies don't) |
| Secrets without a backend | `${pass:…}` needs `pass` on `PATH`; `${ENV_VAR}` needs the var set |
| Moving/renaming the repo | skill symlinks are absolute — delete stale links and reapply after a move |
| Adapters beyond OpenCode | none; configs naming `claude`/`codex` fail at load citing the v0.13.0 removal |

## Where to next

- [Configuration reference](configuration.md) — every table and field of `homonto.toml`.
- [homonto CLI reference](cli-reference.md) — flags, exit codes, examples.
- [Projection & state](projection-and-state.md) — how apply, drift, adoption, and pruning work.
- [Troubleshooting & caveats](troubleshooting.md) — when something looks wrong.
