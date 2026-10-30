# Secrets

Never write a secret value in `homonto.toml`. Write a **reference**; homonto
resolves it only while applying and never stores what it resolved.

References work in the values homonto manages, such as
`[settings.opencode]`. MCP servers are bring-your-own: reference their secrets
in your own `opencode.jsonc` with OpenCode's `{env:VAR}` substitution, which
keeps values out of that file too.

## Reference forms

```toml
[settings.opencode.provider.brave.options]
apiKey = "${pass:ai/brave}"            # from the pass store

[settings.opencode.provider.team.options]
apiKey = "${TEAM_API_KEY}"             # from the environment
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
[settings.opencode.provider.linear.options]
apiKey = "${LINEAR_API_KEY}"
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

The resolved value **is** written into OpenCode's config file — OpenCode
needs it there. Settings land in your global `~/.config/opencode/opencode.jsonc`.
The guarantee covers the files you commit and share: `homonto.toml` and
`.homonto/state.json`. Treat `~/.config/opencode/` accordingly.
