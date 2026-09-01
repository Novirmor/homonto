# Release checklist

The repeatable steps for cutting a triple-binary `homonto` + `onto` + `to`
release. This is the operational *how* of a release; the release gate below
decides *whether*.

Releases are driven by the `release` GitHub workflow
(`.github/workflows/release.yml`), which triggers on any pushed `v*` tag. Do
not push a tag until that workflow packages all three binaries. The workflow
must re-run the CI gates, cross-compile every target, write checksums for
the archives, and publish a GitHub release.

## Pre-tag verification

**`./scripts/gate.sh` is the whole gate**: one command, run identically by
`ci.yml`, by `release.yml` before it builds or publishes anything, and by
you before tagging. That is what makes a tag unable to publish on a weaker
gate than a pull request. It ends with `ALL GATE CHECKS PASSED`.

```sh
./scripts/gate.sh
```

It covers gofmt, `go mod tidy -diff`, vet, build, test, `-race`, version
stamps, a CLI smoke, govulncheck, and the triple-binary Docker E2E. That E2E
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
  and the onto-xor-to exclusivity error.

> **Dogfooding is deferred to v1.** This repository is developed directly
> on branches, with no external workflow stack
> ([ADR 0023](adr/0023-develop-directly-without-comet.md);
> [`personas.md`](personas.md)); onto is the workflow we *ship*, not the one
> we use here yet. The repo therefore carries **no `homonto.toml` and no
> projected `.claude/` / `.opencode/` content** of its own, there is
> deliberately **no "does the repo dogfood cleanly" pre-tag step**, and a
> stale `.homonto/` in a working copy is not a release blocker. The Docker E2E verifies all three binaries in a clean
> environment instead — stronger evidence than a developer machine whose
> state has accumulated across versions.

## Tag and publish

1. Pick the version. Pre-releases use a suffix (`v0.1.0-rc.1`); a bare
   `vMAJOR.MINOR.PATCH` is a full release. The workflow marks any tag
   containing `-` as a GitHub pre-release automatically.
2. Tag an annotated tag on the commit that passed verification, and push
   it:

   ```sh
   git tag -a v0.1.0-rc.1 -m "v0.1.0-rc.1"
   git push origin v0.1.0-rc.1
   ```

3. The `release` workflow then:
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
GOBIN=$(mktemp -d)
export GOBIN
go install github.com/noviopenworks/homonto@v0.1.0-rc.1
go install github.com/noviopenworks/homonto/cmd/onto@v0.1.0-rc.1  # update if final path differs
go install github.com/noviopenworks/homonto/cmd/to@v0.1.0-rc.1
export PATH="$GOBIN:$PATH"
"$GOBIN"/homonto version    # expect the tagged version string
"$GOBIN"/onto version       # expect the tagged version string
"$GOBIN"/to version         # expect the tagged version string
```

Then exercise the binaries against a disposable home:

```sh
HOME=$(mktemp -d) # (or run in a container)
export HOME
homonto init
# edit the generated homonto.toml minimally
homonto plan
homonto apply --yes
homonto status              # expect: No drift
homonto doctor
onto status
onto doctor
to status
```

Verify a downloaded archive's checksum matches `SHA256SUMS`:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

## Rollback

A release is only ever additive to git history, so rollback is deletion
plus a follow-up, never a force-push:

1. Mark the bad GitHub release as a draft (or delete it) so it stops being
   offered:

   ```sh
   gh release delete v0.1.0-rc.1 --yes
   ```

2. Delete the tag locally and on the remote:

   ```sh
   git tag -d v0.1.0-rc.1
   git push origin :refs/tags/v0.1.0-rc.1
   ```

3. `go install ...@v0.1.0-rc.1` keeps working for anyone who already
   resolved it (the module proxy caches tags), so a broken release is
   corrected by shipping a higher patch/rc tag, not by expecting the old
   one to vanish. Never re-point an existing tag at a different commit.

## Security scanning decision (CodeQL / dependency-review)

For the v0.1.0 line, CodeQL and dependency-review are **deferred**, and
this is intentional rather than an oversight:

- `govulncheck` already runs in CI and scans both dependencies and the
  standard library for *called* known vulnerabilities — the highest-signal
  check for a small Go CLI with a tiny dependency set (`cobra`, `go-toml`,
  `sjson`/`gjson`).
- CodeQL's value grows with codebase size and untrusted input surfaces;
  homonto reads a local TOML the user owns and writes local files, so the
  marginal find rate over `go vet` + `govulncheck` is low for now.
- dependency-review gates *new* dependencies in PRs; with a near-static
  dependency list its overhead outweighs its signal today.

Revisit both when the dependency surface grows or the tool starts handling
more untrusted remote input.
