# Release checklist

How to cut a release of the three binaries, `homonto`, `onto`, and `to`.
The gate below decides *whether* to release; the rest of this page is *how*.

## 0. Evidence manifest

Every check this page names is recorded in one evidence manifest, keyed to
the exact candidate commit. A tag is allowed only when the manifest says so.
It is not inferred from green unit tests, and a skipped or unavailable live
check stays visibly incomplete:

```sh
go run ./scripts/releaseevidence init --version vX.Y.Z --out ../evidence-vX.Y.Z.json
# after each check, at the same commit with a clean tree:
go run ./scripts/releaseevidence record ../evidence-vX.Y.Z.json gate/full pass --log <where the log is kept>
go run ./scripts/releaseevidence check ../evidence-vX.Y.Z.json   # exits 1 until every key passed
```

The required keys are the gate's own (`gate/full`, `compat/*`, `live/*`,
`extension/published`, `docs/limitations`) and, for every host × harness cell
in [supported combinations](guides/supported-combinations.md), the evidence
that cell declares (`unit/starter`, `install/first-task`, `e2e/*`,
`sandbox/<host>`). `record` refuses another commit or a dirty tree. Only
`compat/upgrade` may be `n/a`, with a note, when there is no earlier 1.x
release to upgrade from. Keep the manifest outside the repository, because
the tagged commit cannot contain evidence about itself, and attach it to the
GitHub release. `check` also lists the cells the manifest can claim, and the
release notes claim those cells and no others.

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
| `h-surface` | The standalone h-workflow-gh workflow: command and worker projection, worker permissions, rejection by the engine binaries' gates |
| `release-packaging` | The real release build: 18 archives, `SHA256SUMS`, extraction, stamped versions, a smoke of an *extracted* binary |
| `v015-surface` | explain and provenance, links surviving a repo move, evidence and trace, promote, permission suggestions, snapshot undo |

### Compatibility (1.x)

From 1.0 on, every release keeps the 1.x promise
([ADR 0109](adr/0109-keep-1x-compatible-within-the-major.md)). Check each row
for the candidate:

| Surface | Check | Evidence |
|---|---|---|
| Config, state, contracts | `go test ./internal/compat/` passes (part of the gate's `go test`) | gate log |
| Frozen fixtures | `task compat-fixtures` (needs tags; `scripts/compat-fixtures-check.sh <previous tag>`): `testdata/compat/` only gained files | command output |
| New format versions | each format version the release introduces has a new `testdata/compat/<version>/` directory and an `internal/compat` test | diff |
| Deprecations | every deprecated key, flag, value or JSON field warns in `plan`/`doctor`, names its replacement, and is listed in the release notes. Nothing deprecated is removed before 2.0 | release notes |
| Upgrade in place | with the previous 1.x release, `homonto init` + `apply` a disposable project, start an onto change and a to task, then switch to the candidate binaries. `homonto plan` shows only expected changes, `homonto status` reports no drift after apply, and `onto status`/`to status` list the in-flight work | transcript |
| Contract versions | `homonto package contracts` matches [contracts › versions](contracts/README.md#versions). No contract major bump within 1.x | command output |
| Harness and host matrix | the release notes list every host × harness version × engine cell this release was verified on (section 2), and nothing else is claimed | release notes |

A release that cannot meet a row is not a 1.x release. It waits, or it
becomes 2.0.

### Published extension (`extension/published`)

The reference set is published separately at
[Novirmor/homonto-reference-set](https://github.com/Novirmor/homonto-reference-set).
[`examples/reference-set/published.toml`](../examples/reference-set/published.toml)
pins the release the `reference` E2E cell installs: today `v0.1.0`, commit
`37931ba`, content digests `ticket-engine`
`sha256:7ea9996c1f35e01d0e60174daa4bf548c8e42f0609be79ea6518079ad7126143`,
`ticket-workflow`
`sha256:1a20b9c7506926203500f1fe5c326c3a14439e5f5eaab07dfd6aeaaee2525b80`,
`ticket-service`
`sha256:3738da490229a98a29e72dde2fb6bf896026e1bb0a39648b32cf08a4dac3fc8f`.
`TestReferenceSetMatchesItsPublishedPins` (part of the gate's `go test`) fails
when the in-repo copy drifts from those pins. Record `extension/published` as
passing only when all of these hold:

1. `HOMONTO_REF` in the published repository's `.github/workflows/ci.yml` and
   `release.yml` is the release candidate commit, and its `ci` run is green.
   Its `release` workflow re-runs that check for every tag it publishes.
2. `task reference-published` passes. It downloads the pinned assets and
   checks their archive hashes, their content digests and the release's own
   `pins.toml` against `published.toml`.
3. The `reference` cell passes, and its `EVIDENCE reference:` line names the
   pinned tag and digests.

If the release changed the package contract, update the published repository
first, cut a new tag there, and move `published.toml` and the in-repo copy to
it in one commit.

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
3. **Hosting sandboxes** (evidence keys `sandbox/<host>`; `scripts/hosting-sandbox.sh`, fixtures in
   `test/e2e/hosting/sandbox.env`): for each of GitHub, GitLab.com and
   Bitbucket Cloud whose release content changed, the runtime layer (draft,
   unapproved publish is a no-op, native approval, publish, reconcile,
   cleanup against the real host) and the process layer (every read-only
   `/h-*` process ends at a shown draft with nothing published). With
   `--write`, the write processes push to and then reset their fixtures.
   Throwaway repositories and accounts only, never a production issue or
   PR. A host without a sandbox reports SKIP, and a SKIP is not a pass. The
   output links [hosting provider differences](guides/hosting-differences.md);
   confirm every **(unverified)** claim the run exercised and update the
   page.
4. **Real-harness matrix** (`scripts/e2e-matrix.sh`, [test/e2e](../test/e2e/README.md);
   evidence keys `e2e/<cell>`):
   real OpenCode 2 and Claude Code in a container, reusing the operator's
   mounted accounts read-only. Every cell must pass for a release that
   changes an adapter, the runtime core, the doctrine or the package
   contract:

   | Cell | Proves |
   |---|---|
   | `projection/opencode` | apply next to a bring-your-own server and skill; OpenCode reads the config back |
   | `projection/claude` | the builtins project natively for both harnesses; an untrusted folder does not start the MCP server; once trusted, Claude Code connects to `homonto runtime mcp`; doctor reports the tier and ADR 0105 limitations |
   | `onto` | the full onto lifecycle through the real binaries |
   | `reference` | the published reference set release pinned in `examples/reference-set/published.toml` (`homonto-reference-set` `v0.1.0`), fetched over https as digest-pinned `remote:` sources on both harnesses, through its engine's native lifecycle: start, bind, task create, resume, integration check, snapshot, finish, release; record its `EVIDENCE reference:` line |
   | `live/opencode` | OpenCode answers on the mounted account |
   | `recovery/opencode` | an active change reaches a new and a continued OpenCode session through the workflow plugin |
   | `recovery/claude` | an active change reaches a Claude Code session at startup, after `--resume` and after `/compact`; the session calls `homonto_status` over MCP |
   | `policy/claude` | Claude Code reports the coordinator and its worker to homonto's hooks; a worker's denied push is stopped and an allowed command runs on the hook's decision; a refused worker permission prompt is handed back to the coordinator |

5. **Migration both ways** in throwaway projects. OpenCode 2 reads supported
   V1 server config, but its terminal settings live in the global `cli.json`;
   V1 files stay untouched, and snapshot undo does not restore their terminal
   values.

A check that was not run is not a pass. If the owner approves releasing
without one, name every skipped check in the release notes and do not claim
it. That approval covers one tag, never later ones. For a 1.x release the
evidence manifest must still pass `check` for every cell the notes claim.
A cell whose evidence is incomplete is dropped from the supported table in
that release's notes, not claimed with a caveat.

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
   it must describe only this tag. Review the known limitations too. It names
   the host × harness × engine cells the evidence manifest can claim, and the
   accepted Claude Code limitations
   ([ADR 0105](adr/0105-accept-limited-claude-code-capabilities-per-guarantee.md)).
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
go install "github.com/Novirmor/homonto@$TAG"
go install "github.com/Novirmor/homonto/cmd/onto@$TAG"
go install "github.com/Novirmor/homonto/cmd/to@$TAG"
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
  "https://raw.githubusercontent.com/Novirmor/homonto/$TAG/scripts/install.sh"
printf '%s\nnone\n%s\nn\n' "$TAG" "$TMPBIN" | bash /tmp/homonto-install.sh
"$TMPBIN"/homonto version
```

**mise.** Use the three-alias configuration from the
[installation guide](guides/installation.md#mise-recommended) with every
version pinned to the new tag without its `v`, run `mise install`, and run
each binary's `version` through `mise exec --`. Confirm mise picked the
matching `homonto_`, `onto_`, and `to_` archives and all three print the tag.

**A ready-made setup** for each harness, from install to the first task
(`scripts/install-test.sh` t40 does the same offline, before tagging):

```sh
HOME=$(mktemp -d); export HOME
cd "$(mktemp -d)" && git init -q . && git commit -q --allow-empty -m init
homonto init --harness opencode --harness claude --host github --engine onto --engine to \
  --model opencode=anthropic/claude-sonnet-5-5 --model claude=sonnet
homonto plan && homonto apply --yes && homonto status   # No drift.
onto init && onto new first-change && to new first-task
homonto doctor              # names the runtime MCP registration and Claude Code limitations
```

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
