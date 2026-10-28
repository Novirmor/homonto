# Release checklist

How to cut a release of the three binaries, `homonto`, `onto`, and `to`.
The gate below decides *whether* to release; the rest of this page is *how*.

Releases run through the `release` GitHub workflow
(`.github/workflows/release.yml`), triggered by any pushed `v*` tag. Push a tag
only after the local gate passes. The workflow then re-runs the gate,
cross-compiles every target, checksums the archives, and publishes the GitHub
release.

## 1. Pre-tag verification

`./scripts/gate.sh` (`task gate`) is the whole gate. `ci.yml` runs it on every
push and pull request, `release.yml` runs it before building anything, and you
run it before tagging, so a tag can never publish on a weaker check than a pull
request. It needs Go and a running Docker daemon, and it ends with
`ALL GATE CHECKS PASSED`.

```sh
./scripts/gate.sh
```

In order, it runs: gofmt, `go mod tidy -diff`, vet, golangci-lint, build,
tests, tests under `-race`, version-stamp smoke for all three binaries, a CLI
plan smoke, the doc-reference check, the skill shell-out check, the OpenCode
permission-telemetry contract, the installer's mocked-network tests,
govulncheck, and the Docker E2E suites. The suites
(`test/docker/run-all.sh`) run against a disposable `$HOME`, so the host is
never touched:

| Suite | Proves |
|---|---|
| `homonto-core` | `apply` against a real `os.UserHomeDir()` in a throwaway home |
| `homonto-expanded` | Builtin package materialization, skill/command/agent links, agent renders, plugin and TUI projection |
| `multi-repo` | Repo-tagged resources land in declared sibling repos only; per-repo state partitions; undeclared repos untouched |
| `workspace-layout` | Schema-2 control, records, sources, and execution roots through the real CLIs |
| `onto-lifecycle` | onto's install gate, init, gated phase advances (including a failing gate), doctor, dependency-aware close, archive |
| `to-lifecycle` | to's install gate, the self-asserted `--verified`, abandon, archive, config-free read-only commands, onto + to side by side |
| `h-surface` | The standalone h workflow: command and worker projection, worker permissions, rejection by the engine binaries' gates |
| `release-packaging` | The real release build: 18 archives, `SHA256SUMS`, extraction, stamped versions, a smoke of an *extracted* binary |
| `v015-surface` | explain and provenance, links surviving a repo move, evidence and trace, promote, permission suggestions, snapshot undo |

## 2. Live host checks

The gate never boots a real OpenCode 2 host or publishes to GitHub. When a
release changes the workflow plugin, the GitHub draft tools, permission
projection, or the V1 → V2 migration path, run these before choosing the tag
commit, and keep the evidence:

1. **OpenCode 2 host.** With a disposable home and config and the OpenCode
   version the [OpenCode integration guide](guides/opencode.md) names, check
   the project-local V2 plugin directory, the terminal panel, reload and
   unload, the status and handoff tools, model context and compaction, idle
   notifications, and the declared permission observer. Confirm apply removes
   old V1 plugin links. Changing config alone does not hot-reload the client.
2. **GitHub draft denial and approval** with the host's native question and
   exact permission prompts. Unknown sessions, moved directories, changed
   bindings, cancellation, and a service mismatch must fail closed. A
   standalone, unregistered server must not be able to authorize publication.
   A local model with a fake `gh` proves no external send happened, not that
   GitHub accepted a receipt.
3. **Real GitHub sandbox** in a throwaway repository and account: a denied
   send, one permitted send with a matching receipt, changed actor,
   destination, and PR OIDs, and reconciliation of an uncertain send. Never
   use a production issue or PR.
4. **Migration both ways** in throwaway projects. OpenCode 2 reads supported
   V1 server config, but its terminal settings live in the global `cli.json`;
   V1 files stay untouched, and snapshot undo does not restore their terminal
   values.

A check that was not run is not a pass. If the owner approves releasing
without one, name every skipped check in the release notes and do not claim
it. That approval covers one tag, never later ones.

> **No dogfooding step.** This repository is developed directly on branches
> ([ADR 0023](adr/0023-develop-directly-without-comet.md);
> [README › Contributing](../README.md#contributing)). It carries no
> `homonto.toml` and no projected `.opencode/` content, so there is
> deliberately no "does the repo dogfood cleanly" check, and a stale
> `.homonto/` in a working copy is not a release blocker. The Docker suites
> verify all three binaries in a clean environment instead, which is stronger
> evidence than a developer machine with years of accumulated state.

## 3. Release notes

1. Pick the version. A bare `vMAJOR.MINOR.PATCH` is a full release. A suffix
   (`v1.2.0-rc.1`) is a pre-release, and the workflow marks any tag containing
   `-` as a GitHub pre-release.
2. Bump `catalog/version.txt` and, for each package whose content changed, its
   `version` in `catalog/frameworks/<name>/framework.toml`.
3. In [`CHANGELOG.md`](../CHANGELOG.md), retitle the `Unreleased` section as
   `vX.Y.Z — <theme>` and state the stamped binary and catalog versions.
4. In [`release-notes.md`](release-notes.md), replace the release section
   with that entry. The workflow publishes this file as the release body, so
   it must describe only this tag. Review the known limitations too.
5. Re-run `./scripts/gate.sh` after the last change. Only a fully green gate,
   including `release-packaging`, makes a tag candidate.

## 4. Tag and publish

Tag the verified commit with an annotated tag and push it:

```sh
TAG=vX.Y.Z
git tag -a "$TAG" -m "$TAG"
git push origin "$TAG"
```

The `release` workflow then:

- re-runs the full gate;
- builds `homonto`, `onto`, and `to` for `linux`, `darwin`, and `windows` on
  `amd64` and `arm64` (`scripts/build-release.sh`), stamping the tag into each
  `version` command via `-ldflags`;
- archives each binary per target as `<bin>_<tag>_<os>_<arch>.tar.gz`
  (`.zip` for Windows), each with `LICENSE` and `README.md`;
- writes one `SHA256SUMS` over all 18 archives;
- creates the GitHub release from `docs/release-notes.md` plus generated
  notes, with every asset attached.

## 5. Post-tag smoke

Run all of these from **outside** the repository, in a clean environment.

**Go install** of all three command packages at the tag:

```sh
TAG=vX.Y.Z
GOBIN=$(mktemp -d); export GOBIN
go install "github.com/noviopenworks/homonto@$TAG"
go install "github.com/noviopenworks/homonto/cmd/onto@$TAG"
go install "github.com/noviopenworks/homonto/cmd/to@$TAG"
"$GOBIN"/homonto version    # each prints the tag
"$GOBIN"/onto version
"$GOBIN"/to version
```

**The interactive installer** (Linux, macOS) against the same tag. It must
download the tagged script, verify the real archives against `SHA256SUMS`, and
install into the temp directory. The piped answers are: version, no workflow
binaries, install directory, decline initialization.

```sh
TAG=vX.Y.Z
TMPBIN=$(mktemp -d)
curl -fsSL -o /tmp/homonto-install.sh \
  "https://raw.githubusercontent.com/noviopenworks/homonto/$TAG/scripts/install.sh"
printf '%s\nnone\n%s\nn\n' "$TAG" "$TMPBIN" | bash /tmp/homonto-install.sh
"$TMPBIN"/homonto version
```

**mise.** Use the three-alias configuration from the
[installation guide](guides/installation.md#mise-recommended) with every
version pinned to the new tag without its `v`, run `mise install`, and run
each binary's `version` through `mise exec --`. Confirm mise picked the
matching `homonto_`, `onto_`, and `to_` archives and all three print the tag.

**A disposable home:**

```sh
HOME=$(mktemp -d); export HOME
cd "$(mktemp -d)" && git init -q .
homonto init
homonto plan                # No changes. Everything up to date.
homonto apply --yes
homonto status              # No drift.
homonto doctor              # three warnings, see below; exit 0
onto status                 # no active changes
to status                   # No active changes
```

Two results look like failures and are not:

- **`homonto doctor` warns three times**: `catalog upgrade pending: recorded
  (none)` (the generated `homonto.toml` declares nothing yet), `pass` not on
  `PATH`, and OpenCode's config location not found. All three describe the
  empty environment, not the release.
- **`onto doctor` exits 1 before `onto init`**: it reports the four missing
  `docs/{changes,specs,adr,guides}` directories. `onto status` and
  `onto doctor` run anywhere, which is why they don't assume a scaffolded
  repository.

To smoke onto itself, install its engine first, since `onto init` is behind
the install gate. Every agent the package installs needs a model block, or the
config fails to load:

```sh
cat >> homonto.toml <<'TOML'
[engines.onto]
source = "builtin:onto"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
TOML
homonto apply --yes
onto init
onto doctor                 # healthy
```

For `to`, add `[engines.to]` and model blocks for `to-explorer`,
`to-implementer`, `to-reviewer`, and `to-skeptic` (the `homonto` block is
shared), then `to init` and `to doctor`. Both engines may be declared in one
config.

To check a downloaded archive by hand:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

## Rollback

A release only ever adds to Git history, so rollback is deletion plus a
follow-up release, never a force-push:

1. Stop offering the release (or mark it as a draft in the GitHub UI):

   ```sh
   TAG=vX.Y.Z
   gh release delete "$TAG" --yes
   ```

2. Delete the tag locally and on the remote:

   ```sh
   git tag -d "$TAG"
   git push origin ":refs/tags/$TAG"
   ```

3. Ship a higher patch or rc tag with the fix. `go install …@vX.Y.Z` keeps
   working for anyone who already resolved it, because the module proxy
   caches tags, so the old version will not vanish. Never re-point an
   existing tag at a different commit.

## Security scanning decision

CodeQL and dependency-review are **deferred** on purpose:

- `govulncheck` runs in the gate and reports *called* known vulnerabilities in
  dependencies and the standard library, the highest-signal check for a Go CLI
  with six direct dependencies. golangci-lint (staticcheck, govet, errcheck)
  covers static analysis.
- dependency-review gates *new* dependencies in pull requests. With a
  near-static dependency list, its overhead outweighs its signal.

Revisit both when the dependency surface grows. Remote sources are already
treated as untrusted input and verified fail-closed against a sha256 pin
([ADR 0013](adr/0013-remote-source-trust-boundary.md)).
