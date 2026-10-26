# Install with mise

[mise's GitHub backend](https://mise.jdx.dev/dev-tools/backends/github.html)
installs homonto's prebuilt GitHub releases for Linux, macOS, and Windows on
amd64/x64 and arm64. It selects the platform archive, verifies the download
against release checksums, and manages the executable's version and PATH.
Use a current mise; the examples below were verified with mise 2026.10.19.

## Install homonto

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@latest'
mise exec -- homonto version
```

The asset filter selects the `homonto_` archive from the three binaries in each
release. `-g` writes your global mise configuration. Omit it to record the tool
in the current project's `mise.toml` instead. With mise activated in your shell,
you can run `homonto` directly; `mise exec --` also works without activation.

To pin a published version, replace `latest` with its number, for example:

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@0.33.0'
```

mise versions omit the release tag's leading `v`. Installation uses a published
release, so unreleased branch changes become available after a release is cut.

## Install onto and to

Each release provides separate archives. Give each binary its own mise alias
and an exact asset-prefix filter. Put this in your project's `mise.toml`, or
merge it into your global `~/.config/mise/config.toml`:

```toml
[tool_alias]
homonto = "github:Novirmor/homonto"
onto = "github:Novirmor/homonto"
to = "github:Novirmor/homonto"

[tools]
homonto = { version = "latest", matching_regex = "^homonto_" }
onto = { version = "latest", matching_regex = "^onto_" }
to = { version = "latest", matching_regex = "^to_" }
```

When moving from the single-binary setup above, replace its
`"github:Novirmor/homonto"` tool entry with these alias entries. Keep only the
companions you need. The anchors matter: `onto_` and `to_` also occur inside
`homonto_`. Distinct aliases give each binary its own installation directory;
multiple options on the same `github:Novirmor/homonto` tool key do not.

```sh
mise install
mise exec -- homonto version
mise exec -- onto version
mise exec -- to version
```

For reproducible versions, replace every `latest` with the same release number.
After installing, follow [first steps](../../README.md#first-steps) to configure
the packages you want in `homonto.toml` and apply them.

## Upgrade

For the single-binary configuration, select the newest release again:

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@latest'
```

For the alias configuration, update all selected binaries together:

```sh
mise use -g homonto@latest onto@latest to@latest
```

Omit `-g` for project-local configuration. Run `homonto update` in each managed
workspace afterward to update its projected catalog. Review any release-specific
configuration migration instructions before applying a newer release.
