# Release Homonto

Use this procedure to prepare and publish a tagged binary release. It covers
release evidence and artifacts; it does not expose a self-update command to
users.

## Run The Release Gate

Run the full gate from a clean release candidate:

```bash
./scripts/gate.sh
```

The full gate checks formatting, module state, vet, builds, tests, race tests,
release packaging, security suites, fuzzing, and vulnerability scanning. Use
`./scripts/gate.sh --quick` only for local iteration; it is not a release gate.

## Prepare Release Evidence

Create `.release-evidence/` with the host matrix and two non-empty rehearsal
records:

```text
.release-evidence/host-cells.tsv
.release-evidence/migration-rehearsal.txt
.release-evidence/rollback-rehearsal.txt
```

`host-cells.tsv` must contain `pass` for each supported host and operating
system:

The file is tab-separated. Replace `<TAB>` below with a literal tab character.

```text
claude<TAB>linux<TAB>pass
claude<TAB>darwin<TAB>pass
opencode<TAB>linux<TAB>pass
opencode<TAB>darwin<TAB>pass
```

The release guard checks those four host/operating-system cells. Record the
scenarios you exercised in the evidence files; the guard verifies that required
cells are present and marked `pass`, not that every workflow scenario ran.

For the workflow product's first release, record the clean-break migration
decision and [ADR 0023](../adr/0023-rebuild-homonto-as-workflow-orchestrator.md)
in `migration-rehearsal.txt`. Later releases must record the source schema,
migration, and verification. `rollback-rehearsal.txt` records a deliberately
failed activation and its exact rollback.

Provide the release key, its root ID, and publish credentials, then run the
guard:

```bash
export HOMONTO_RELEASE_SIGNING_KEY=/path/to/root.key
export HOMONTO_RELEASE_ROOT_ID=homonto-release-1
export GH_TOKEN=...
./scripts/release-guard.sh --evidence .release-evidence
```

Commit the evidence before tagging so the release workflow can copy it:

```bash
git add .release-evidence
git commit -m "docs: record release evidence"
```

## Publish The Tag

Create and push a version that sorts above `v0.11.0`:

```bash
git tag -a <new-version> -m "<new-version>"
git push origin <new-version>
```

Pushing a `v*` tag starts `.github/workflows/release.yml`. The workflow runs
the full gate, copies committed release evidence, runs the release guard, builds
the Linux and macOS archives, signs the manifest, and publishes the GitHub
release. A failure after the tag push leaves the tag public but prevents a
completed release publication. A version containing `-` publishes as a
pre-release.

## Verify Published Artifacts

Download a published archive and its checksum list, then verify the archive:

```bash
curl -fsSLO <release-url>/SHA256SUMS
curl -fsSLO <release-url>/homonto_<version>_linux_amd64.tar.gz
sha256sum -c --ignore-missing SHA256SUMS
```

The release publishes four archives: Linux and macOS on amd64 and arm64, plus
`SHA256SUMS`, `release-manifest.json`, and `release-manifest.sig`. Do not test a
published release with `homonto update`; the user-operable self-update command
does not exist.
