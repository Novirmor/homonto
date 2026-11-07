# Installation

homonto ships three binaries from one repository and one release:

| Binary | You need it when |
|---|---|
| `homonto` | Always. It reads `homonto.toml` and projects it into OpenCode. |
| `onto` | You declare `[engines.onto]` (the evidence-gated workflow). |
| `to` | You declare `[engines.to]` (the minimal workflow). |

The `no` engine and the `h-workflow-gh` GitHub workflow have no binary of their own.

Every tagged release attaches one archive per binary per platform
(`<binary>_<tag>_<os>_<arch>.tar.gz`, `.zip` on Windows) for Linux, macOS, and
Windows on amd64 and arm64, plus one `SHA256SUMS` file covering all of them.

Pick one installation method:

- [mise](#mise-recommended) — recommended; manages versions and PATH for you.
- [Interactive installer](#interactive-installer) — Linux/macOS, no extra tools.
- [Release archives](#release-archives) — download and verify by hand.
- [Go toolchain](#go-install) — build from source.

After any of them, [verify the install](#verify) and run
[first steps](getting-started.md).

## Next test releases

Experimental builds of the `next` branch appear as pre-releases named
`homonto-next-N` on the [releases page](https://github.com/Novirmor/homonto/releases).
They are independently numbered and do not replace the latest stable release.
The interactive installer accepts semantic versions, not these names; use the
archives instead. For example, on Linux amd64, download the `homonto`, `onto`,
and `to` archives you need and `SHA256SUMS` from the same pre-release, then:

```sh
TAG=homonto-next-1  # replace with the chosen release
sha256sum -c SHA256SUMS --ignore-missing  # macOS: shasum -a 256 -c SHA256SUMS
tar -xzf "homonto_${TAG}_linux_amd64.tar.gz"
./"homonto_${TAG}_linux_amd64"/homonto version
```

On macOS, run the checksum command in a directory containing all 18 archives,
or verify the downloaded files against their entries in `SHA256SUMS` manually;
`shasum` does not ignore missing files. Choose the appropriate OS and
architecture archive; Windows uses ZIP files. Keep test binaries in a
separate directory on your PATH. To return to stable, remove that directory
from PATH or reinstall a stable release using the methods below. Report issues
on the project's issue tracker with the release name and commit from its notes.
These names are not Go semantic versions; use archives rather than `go install`.

## mise (recommended)

[mise](https://mise.jdx.dev/) installs prebuilt GitHub releases through its
[GitHub backend](https://mise.jdx.dev/dev-tools/backends/github.html): it picks
the archive for your platform, verifies its checksum, and puts the executable
on PATH.

> **Use a current mise.** The examples below were verified with mise
> 2026.10.19. Older releases (for example 2026.3.13) ignore `matching_regex`
> and install the `homonto` archive under every alias, so `onto` and `to`
> would be missing. Run `mise self-update` first if in doubt.

### Just homonto

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@latest'
homonto version            # or: mise exec -- homonto version
```

The `matching=homonto_` filter picks the `homonto_…` archive out of the
release's three archives. `-g` writes your global config
(`~/.config/mise/config.toml`):

```toml
[tools]
"github:Novirmor/homonto" = { version = "0.34.0", matching = "homonto_" }
```

Without `-g`, the same entry goes into the current directory's `mise.toml`,
so the version travels with the project:

```sh
cd ~/work/my-project
mise use 'github:Novirmor/homonto[matching=homonto_]@latest'
```

### homonto, onto, and to together

Each binary is its own archive, so each needs its own mise alias and an
**anchored** asset filter. Put this in a project `mise.toml`, or merge it into
`~/.config/mise/config.toml` for a global install:

```toml
[tool_alias]
homonto = "github:Novirmor/homonto"
onto    = "github:Novirmor/homonto"
to      = "github:Novirmor/homonto"

[tools]
homonto = { version = "latest", matching_regex = "^homonto_" }
onto    = { version = "latest", matching_regex = "^onto_" }
to      = { version = "latest", matching_regex = "^to_" }
```

```sh
mise install
homonto version     # homonto v0.34.0
onto version        # onto v0.34.0
to version          # to v0.34.0
```

Why the anchors matter: `onto_` and `to_` are substrings of `homonto_`, so an
unanchored filter could select the wrong archive. Why the aliases matter: they
give each binary its own install directory; three options on one
`github:Novirmor/homonto` key would not.

Keep only the binaries you use. A project that only uses the `to` engine:

```toml
[tool_alias]
homonto = "github:Novirmor/homonto"
to      = "github:Novirmor/homonto"

[tools]
homonto = { version = "latest", matching_regex = "^homonto_" }
to      = { version = "latest", matching_regex = "^to_" }
```

If you previously used the single-binary entry, delete its
`"github:Novirmor/homonto"` line when you switch to the aliases.

### Pin a version

mise versions drop the tag's leading `v`. Pin all binaries to the same
release — a workflow binary and the homonto that installed its skills should
match (`onto doctor` and `to doctor` report skew):

```toml
[tools]
homonto = { version = "0.34.0", matching_regex = "^homonto_" }
onto    = { version = "0.34.0", matching_regex = "^onto_" }
to      = { version = "0.34.0", matching_regex = "^to_" }
```

Single-binary form:

```sh
mise use -g 'github:Novirmor/homonto[matching=homonto_]@0.34.0'
```

List published versions:

```sh
mise ls-remote github:Novirmor/homonto
```

mise installs **published releases** only. Changes on a branch become
installable after a release is tagged.

### Upgrade

```sh
# single-binary entry
mise use -g 'github:Novirmor/homonto[matching=homonto_]@latest'

# alias entries — upgrade all three together
mise use -g homonto@latest onto@latest to@latest
```

Drop `-g` for a project-local `mise.toml`. Then, in **every** workspace that
homonto manages:

```sh
homonto update      # re-materialize the bundled catalog at the new version
```

A new binary does not change projected skills, commands, or agents until you
run `homonto update` (or `homonto apply`). Read the release's
`CHANGELOG.md` entry for any configuration migration before applying.

### Use it in CI

```yaml
# GitHub Actions
- uses: jdx/mise-action@v2
- run: homonto plan --exit-code   # exit 2 when an apply is pending
```

Commit the project `mise.toml` with pinned versions so CI and every developer
use the same binaries.

## Interactive installer

Linux and macOS (amd64, arm64). Download the script, read it, then run it:

```sh
curl -fsSL -o install.sh https://raw.githubusercontent.com/Novirmor/homonto/main/scripts/install.sh
less install.sh
bash install.sh
```

It asks:

1. **Which release** — latest by default.
2. **Which workflow binaries** — `onto`, `to`, both (default), or none.
3. **Where to install** — `~/.local/bin` by default.

It downloads the archives, verifies each against the release `SHA256SUMS`
(refusing on a mismatch), installs, and **prints** the `export PATH=…` line
for you to add. It never edits your shell profile.

Then it offers to run `homonto init` in the current directory. Only if you
confirm, and only when there is no `homonto.toml` yet, it asks:

- which harness to configure (OpenCode, Claude Code, or both);
- which engines to enable (`onto`, `to`) and which hosting workflow (GitHub,
  GitLab.com, Bitbucket Cloud, or none);
- where workflow records live (`[workflow] root`, default `docs`);
- whether to declare a workspace scratch directory (`[tmp]`);
- which sibling Git repositories to trust (`[repos]`);
- one model per harness, which every workflow agent on it runs on
  (`[routing.default.<harness>]`). Your own OpenCode session model is left
  alone.

The installer only collects the answers. `homonto init --harness … --engine …`
generates and validates the file
([ADR 0110](../adr/0110-generate-starting-configurations-in-the-binary.md)),
so a scripted setup can call it directly. Choosing no engine and no host
writes the commented scaffold instead. Review the generated file before
`homonto apply`.

An existing `homonto.toml` is never modified. Prompts use
[gum](https://github.com/charmbracelet/gum) when installed, then `dialog`,
then plain text. With no stdin every answer takes its default (latest
release, both workflow binaries, `~/.local/bin`, no init):

```sh
bash install.sh </dev/null
```

## Release archives

```sh
TAG=v0.34.0
OS=linux        # linux | darwin | windows
ARCH=amd64      # amd64 | arm64
BASE=https://github.com/Novirmor/homonto/releases/download/$TAG

for bin in homonto onto to; do
  curl -fsSLO "$BASE/${bin}_${TAG}_${OS}_${ARCH}.tar.gz"
done
curl -fsSLO "$BASE/SHA256SUMS"
sha256sum --ignore-missing -c SHA256SUMS     # macOS: shasum -a 256 -c …

for bin in homonto onto to; do
  tar -xzf "${bin}_${TAG}_${OS}_${ARCH}.tar.gz"
  install -m 0755 "${bin}_${TAG}_${OS}_${ARCH}/${bin}" ~/.local/bin/
done
```

Each archive contains the binary, `LICENSE`, and `README.md`.

## go install

Requires the Go toolchain pinned in `go.mod` (or newer). Release builds stamp
their version at link time; a plain `go install` reports a development
version.

```sh
go install github.com/Novirmor/homonto@latest            # homonto
go install github.com/Novirmor/homonto/cmd/onto@latest   # onto
go install github.com/Novirmor/homonto/cmd/to@latest     # to
```

Releases up to v0.36.0 declare the old module path, so `go install` them as
`github.com/noviopenworks/homonto@<tag>`
([ADR 0088](../adr/0088-module-path-matches-canonical-repository.md)).

From a clone, `task install` installs all three with version stamps (see
`Taskfile.yml`). If you keep a local `homonto/` content directory at the
repository root, `go build .` collides with it; build to an explicit path
instead: `go build -o ./bin/homonto .`.

## Verify

```console
$ homonto version
homonto v0.34.0
$ onto version
onto v0.34.0
$ to version
to v0.34.0
```

`homonto --version`, `onto --version`, and `to --version` work too.

All three print human output to **stderr** (the CLI library's default).
Redirect with `2>&1` when capturing output in scripts.

## Shell completion

```sh
homonto completion zsh  > "${fpath[1]}/_homonto"
onto completion bash    > ~/.local/share/bash-completion/completions/onto
to completion fish      > ~/.config/fish/completions/to.fish
```

## Uninstall

1. Remove projected content from a workspace: delete the packages and
   resources from `homonto.toml` and run `homonto apply` — homonto prunes what
   it installed. Delete `.homonto/` afterwards if you no longer need state.
2. Remove the binaries: `mise uninstall homonto onto to` (alias setup) and
   delete their entries from `mise.toml`, or delete the files from the
   install directory.

## Next

- [Getting started](getting-started.md) — your first `homonto.toml`, plan,
  apply, and workflow.
- [Troubleshooting](troubleshooting.md#installing) — PATH, checksum, and
  version problems.
