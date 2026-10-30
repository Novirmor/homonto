# Secrets

Never write a secret value in `homonto.toml`. Write a **reference**; homonto
resolves it only while applying and never stores what it resolved.

## Reference forms

```toml
[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env = { BRAVE_API_KEY = "${pass:ai/brave}" }            # from the pass store

[mcps.github]
command = ["github-mcp-server", "stdio"]
env = { GITHUB_PERSONAL_ACCESS_TOKEN = "${GITHUB_TOKEN}" }   # from the environment
```

| Form | Resolved from | Fails when |
|---|---|---|
| `${pass:path/to/entry}` | [`pass`](https://www.passwordstore.org/) | `pass` isn't on PATH or the entry is missing |
| `${ENV_VAR}` | the process environment | the variable is unset at apply time |

Document environment references in `.env.example` (created by
`homonto init`); keep real values in `.env` (gitignored) or your shell.

## Guarantees

- **`plan` resolves nothing.** It contacts no backend and prints the `${…}`
  token.
- **`apply` resolves everything first,** after confirmation and before any
  write. One failure aborts the whole apply with nothing changed.
- **State stores a hash, not a value.** `.homonto/state.json` keeps the token
  and a sha256 of the resolved value: safe to share, idempotent across
  applies, and still able to detect an out-of-band change.
- **Secret values are never adopted.** A resource containing a reference is
  always re-applied so the resolved value is checked.
- **`explain` never prints values.**

## Examples

Per-developer tokens via the environment:

```toml
[mcps.linear]
command = ["npx", "-y", "@linear/mcp-server"]
env = { LINEAR_API_KEY = "${LINEAR_API_KEY}" }
```

```console
$ LINEAR_API_KEY=lin_… homonto apply
```

A team using `pass`:

```console
$ pass insert ai/brave
$ homonto doctor
ok: pass found
…
$ homonto apply
```

A missing reference:

```console
$ unset LINEAR_API_KEY
$ homonto apply --yes
…
error: env var LINEAR_API_KEY is not set          # nothing was written
```

## What isn't covered

The resolved value **is** written into OpenCode's config file — the MCP
server needs it there. The guarantee covers the files you commit and share:
`homonto.toml` and `.homonto/state.json`. Treat `~/.config/opencode/` and a
project `opencode.jsonc` containing resolved values accordingly (a
project-scoped MCP with a secret writes the value into the repository's
`opencode.jsonc`; keep that file out of Git or use user scope).
