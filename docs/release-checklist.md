# Release checklist

The repeatable steps for cutting a triple-binary `homonto` + `onto` + `to`
release. This is the operational *how* of a release; the release gate below
decides *whether*.

Releases are driven by the `release` GitHub workflow
(`.github/workflows/release.yml`), which triggers on any pushed `v*` tag. Do
not push a tag until the local gate and packaging rehearsal pass. After
the tag is pushed, the workflow must re-run the CI gates, cross-compile every
target, write checksums for the archives, and publish a GitHub release.

## Pre-tag verification

**`./scripts/gate.sh` is the whole gate**: one command, run identically by
`ci.yml`, by `release.yml` before it builds or publishes anything, and by
you before tagging. That is what makes a tag unable to publish on a weaker
gate than a pull request. It ends with `ALL GATE CHECKS PASSED`.

```sh
./scripts/gate.sh
```

It covers gofmt, `go mod tidy -diff`, vet, golangci-lint, build, test,
`-race`, version stamps, a CLI smoke, the doc-reference, skill shell-out, and
permission-telemetry checks, the installer tests, govulncheck, and the
triple-binary Docker E2E. That E2E
is where the old hand-written checks now live, done against a disposable
`$HOME` so the host is never touched:

- **`release-packaging`** — builds every target, verifies `SHA256SUMS` over
  all archives, then extracts the **real** archives and smokes all three
  binaries (`version` reports the stamped tag; `init`/`plan`/`apply`/`status`
  run clean).
- **`homonto-core` / `homonto-expanded`** — projection, per-tool agent
  render, and prune behavior for the builtin catalog.
- **`onto-lifecycle`** — drives a change through onto's gates.
- **`to-lifecycle`** — drives a change through to's plan → do → done: gate
  refusal, the `--verified` requirement, archive, doctor and convergence,
  and onto/to coexistence with shared-resource deduplication.

## Live host checks

The gate does not boot a real OpenCode 2 host or publish to GitHub. When a
release changes the workflow plugin, the GitHub draft tools, permission
projection, or the V1 → V2 migration path, run these before choosing a tag
commit, and keep the evidence:

1. With a disposable home/config and the **OpenCode version** the
   [OpenCode 2 guide](guides/opencode-v2.md) links against, check the project-local V2 plugin directory, terminal panel,
   reload/unload, status/handoff tools, model context and compaction, idle
   notifications, and the declared permission observer. Verify old V1 plugin
   links are removed on apply; changing config alone does not hot-reload the
   client.
2. Test GitHub draft **denial and approval** with the host's native question
   and exact permission prompts. Unknown sessions, moved directories, changed
   bindings, cancellation, and a service mismatch must fail closed. A
   standalone, unregistered server intentionally cannot authorize publication.
   A local-model plus fake-`gh` run proves no external send happened, not that
   real GitHub accepted a receipt.
3. Run a **real GitHub sandbox** in a throwaway repo and account: a denied
   send, a single permitted send with a matching receipt, changed
   actor/destination/PR OIDs, and uncertain-send reconciliation. Never use a
   production issue or PR.
4. Check both migration directions in throwaway projects. OpenCode 2 reads
   supported V1 server config, but its terminal settings live in global
   `cli.json`; V1 files stay untouched and snapshot undo does not restore their
   terminal values.

A check that was not run is not a pass. If the owner approves releasing
without one, name every skipped check in the release notes, and do not claim
it for that release. Such an approval covers one tag, never later ones.

Then review the release section of `docs/release-notes.md` against the final
tag and verification results, and re-run `./scripts/gate.sh` after the last
change. Only a fully green gate, including the release-packaging Docker suite,
makes a tag candidate.

> **Dogfooding is deferred to v1.** This repository is developed directly
> on branches, with no external workflow stack
> ([ADR 0023](adr/0023-develop-directly-without-comet.md);
> [`personas.md`](personas.md)); onto is the workflow we *ship*, not the one
> we use here yet. The repo therefore carries **no `homonto.toml` and no
> projected `.opencode/` content** of its own, there is
> deliberately **no "does the repo dogfood cleanly" pre-tag step**, and a
> stale `.homonto/` in a working copy is not a release blocker. The Docker E2E verifies all three binaries in a clean
> environment instead — stronger evidence than a developer machine whose
> state has accumulated across versions.

## Tag and publish

1. Pick the version. Pre-releases use a suffix (for example,
   `v1.2.0-rc.1`); a bare
   `vMAJOR.MINOR.PATCH` is a full release. The workflow marks any tag
   containing `-` as a GitHub pre-release automatically.
2. Move the previous release's section from `docs/release-notes.md` into
   `CHANGELOG.md`, and write the new one in its place. The release workflow
   publishes that file as the release body, so it must describe only this tag.
3. Tag an annotated tag on the commit that passed verification, and push
   it:

   ```sh
    TAG=vX.Y.Z
    git tag -a "$TAG" -m "$TAG"
    git push origin "$TAG"
   ```

4. The `release` workflow then:
    - re-runs the full gate as a guard,
    - builds `homonto`, `onto`, and `to` for `linux`, `darwin`, and
      `windows` on `amd64` and `arm64`, stamping the tag into every version
      command via `-ldflags`,
    - archives each target (`.tar.gz`, or `.zip` for Windows) with `LICENSE`
      and `README.md`,
    - writes a single `SHA256SUMS` over every archive,
    - creates the GitHub release with generated notes and all assets
      attached.

## Post-tag smoke install

From **outside** the repo, in a clean environment, verify the command
packages install at the tag. Keep the concrete import paths matched to the
release commit layout, and do not tag while this smoke covers only some of
the binaries:

```sh
TAG=vX.Y.Z
GOBIN=$(mktemp -d)
export GOBIN
go install "github.com/noviopenworks/homonto@$TAG"
go install "github.com/noviopenworks/homonto/cmd/onto@$TAG"
go install "github.com/noviopenworks/homonto/cmd/to@$TAG"
export PATH="$GOBIN:$PATH"
"$GOBIN"/homonto version    # expect the tagged version string
"$GOBIN"/onto version       # expect the tagged version string
"$GOBIN"/to version         # expect the tagged version string
```

Then smoke the interactive installer (Linux/macOS) against the same tag: it
must download the tagged script, verify the real release archives against
`SHA256SUMS`, and install into the temp dir (answers: version, no workflow
binary, temp dir, decline initialization):

```sh
TAG=vX.Y.Z
TMPBIN=$(mktemp -d)
curl -fsSL -o /tmp/homonto-install.sh \
  "https://raw.githubusercontent.com/noviopenworks/homonto/$TAG/scripts/install.sh"
printf '%s\nnone\n%s\nn\n' "$TAG" "$TMPBIN" | bash /tmp/homonto-install.sh
"$TMPBIN"/homonto version   # expect the tagged version string
```

Also smoke the [mise installation](guides/mise.md) from a clean environment.
Use the companion-binary configuration with all three versions pinned to the
new tag's version (without its leading `v`), run `mise install`, and run each
binary's `version` through `mise exec --`. Confirm the selected assets are the
corresponding `homonto_`, `onto_`, and `to_` archives and all three report the tag.

Then exercise the binaries against a disposable home:

```sh
HOME=$(mktemp -d) # (or run in a container)
export HOME
homonto init
# edit the generated homonto.toml minimally
homonto plan
homonto apply --yes
homonto status              # expect: No drift
homonto doctor              # warnings only (see below)
onto status                 # expect: no active changes
to status                   # expect: no active changes
```

Two results here look like failures and are not:

- **`homonto doctor` warns.** In a disposable `$HOME` with no editor installed
  it reports `pass` missing from `PATH` and both tool config locations absent,
  and — because a freshly generated `homonto.toml` declares nothing to apply —
  `catalog upgrade pending: recorded (none)`. All four are environment facts,
  not release defects. Read them; do not expect silence.
- **`onto doctor` exits non-zero on an unscaffolded workspace.** A missing
  `docs/{changes,specs,adr,guides}` layout is a documented finding, so the bare
  command reports four problems — `onto status` and `onto doctor` are
  config-independent and run anywhere, which is exactly why they do not imply a
  scaffolded repo.

To smoke `onto` properly you must install its framework first, because
`onto init` is itself gated on it. That is what the "edit the generated
`homonto.toml` minimally" step above means concretely — declare the framework
**and** a `[subagents.<name>.opencode]` model block for each of its five agents
(`homonto`, `onto-explorer`, `onto-reviewer`, `onto-implementer`, `onto-skeptic`),
since a missing block fails at load:

```sh
cat >> homonto.toml <<'TOML'
[engines.onto]
source = "builtin:onto"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
# … one [subagents.<name>.opencode] block per agent
TOML

homonto apply --yes
onto init
onto doctor                 # expect: healthy
```

Replace `[engines.onto]` and the four `onto-*` specialists with `[engines.to]` and the four
`to-*` specialists (the shared `homonto` block stays). The two frameworks
are complementary, so one config may cover both.

Verify a downloaded archive's checksum matches `SHA256SUMS`:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

The interactive installer performs this verification automatically before it
installs anything; the mocked-network installer tests (`scripts/install-test.sh`)
run in the gate.

## Rollback

A release is only ever additive to git history, so rollback is deletion
plus a follow-up, never a force-push:

1. Mark the bad GitHub release as a draft (or delete it) so it stops being
   offered:

   ```sh
   TAG=vX.Y.Z
   gh release delete "$TAG" --yes
   ```

2. Delete the tag locally and on the remote:

   ```sh
   git tag -d "$TAG"
   git push origin ":refs/tags/$TAG"
   ```

3. `go install ...@vX.Y.Z` keeps working for anyone who already
   resolved it (the module proxy caches tags), so a broken release is
   corrected by shipping a higher patch/rc tag, not by expecting the old
   one to vanish. Never re-point an existing tag at a different commit.

## Security scanning decision (CodeQL / dependency-review)

CodeQL and dependency-review are **deferred** on purpose:

- `govulncheck` runs in the gate and reports *called* known vulnerabilities in
  dependencies and the standard library, the highest-signal check for a Go CLI
  with six direct dependencies. golangci-lint (staticcheck, govet, errcheck)
  covers the static side.
- dependency-review gates *new* dependencies in PRs; with a near-static
  dependency list its overhead outweighs its signal.

Revisit both when the dependency surface grows. Remote sources are already
untrusted input; they are verified fail-closed against a sha256 pin
([ADR 0013](adr/0013-remote-source-trust-boundary.md)).
