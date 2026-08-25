# Release checklist

Cutting a `homonto` release. The gate decides whether the code is
shippable; the guard decides whether the *release* was actually prepared.
Both have to pass, and neither can be argued with.

## What gets published

```text
homonto_<version>_linux_amd64.tar.gz
homonto_<version>_linux_arm64.tar.gz
homonto_<version>_darwin_amd64.tar.gz
homonto_<version>_darwin_arm64.tar.gz
SHA256SUMS
release-manifest.json
release-manifest.sig
```

Exactly that, pinned by `test/release/packaging_test.go`. An extra archive
is a platform nobody verified; a missing one is a platform someone will
report as broken.

The manifest is what `homonto update` reads. It is generated from the
archives on disk by `tools/release-manifest`, using the same Go types the
shipped binary parses it with — no field of it is ever hand-written.

## Pre-tag

**`./scripts/gate.sh` is the whole gate.** One command, run identically by
`ci.yml`, by `release.yml` before it builds anything, and by you before
tagging — so a tag cannot publish on a weaker gate than a pull request had
to pass. It ends with `ALL GATE CHECKS PASSED`.

```sh
./scripts/gate.sh
```

It covers gofmt, `go mod tidy -diff`, vet, build, shell syntax, the full
test suite, the race suite, the version stamp, the read-only smoke, release
packaging end-to-end (built, signed, and verified), the documentation and
whole-program security suites, 30 seconds of fuzzing per target, and
`govulncheck`.

`./scripts/gate.sh --quick` skips fuzzing and govulncheck for a fast local
loop. It prints that it is not a release gate, because it is not one.

CI runs the gate on Linux **and** macOS on every push. The filesystem layer
is written against fd-anchored syscalls that differ between the two, and a
divergence there is not something to find at release time.

## The evidence the gate cannot produce

Five things a runner cannot generate, all of which only matter when
something goes wrong, and all of which are therefore the ones that get
waved through:

1. **The live host matrix.** Every workflow scenario run against real
   Claude Code and real OpenCode, on Linux and on macOS. Four cells.
2. **The migration rehearsal.** The previous release's store, migrated to
   this release's schema, and checked.
3. **The rollback rehearsal.** A deliberately failed activation, and the
   exact rollback that followed.
4. **A signing key** and the root id it signs as.
5. **Publish credentials.**

Record them under `.release-evidence/`:

```text
.release-evidence/host-cells.tsv          host TAB platform TAB result
.release-evidence/migration-rehearsal.txt what was migrated and how it was checked
.release-evidence/rollback-rehearsal.txt  the failed activation and the rollback
```

`host-cells.tsv` needs all four cells reading exactly `pass`:

```text
claude	linux	pass
claude	darwin	pass
opencode	linux	pass
opencode	darwin	pass
```

Then:

```sh
export HOMONTO_RELEASE_SIGNING_KEY=/path/to/root.key
export HOMONTO_RELEASE_ROOT_ID=homonto-release-1
export GH_TOKEN=...
./scripts/release-guard.sh --evidence .release-evidence
```

The guard refuses and names every missing piece. **`skip`, `fail`, an
absent line, and an empty rehearsal file all refuse.** That is the point:
a host cell nobody could run — no macOS to hand, no OpenCode installed —
reads exactly like a host cell that passed unless something refuses.

Do not edit the evidence to get past the guard. The guard is not the
obstacle; the unrun cell is.

## Tag and publish

```sh
git tag -a v0.9.0 -m "v0.9.0"
git push origin v0.9.0
```

`release.yml` then runs the gate, unpacks `.release-evidence/`, runs the
guard, builds and packages, signs the manifest with the release key, and
publishes. Every one of those steps can stop the release, and each stops it
before anything is public.

A tag containing `-` (`v0.9.0-rc.1`) publishes as a pre-release and never
shows as latest.

## Signing keys

Generate one with the release tool, which never uploads anything:

```sh
go run ./tools/release-sign keygen --id homonto-release-1 --out root.key
```

The public key goes into `internal/update/trust` as a compiled root of the
*next* build. A build carries the roots it will accept a release from, and
carries them at compile time — which is why rotating a key needs a manifest
signed by the roots already trusted **and** a candidate that actually
carries the new ones.

A locally built binary carries no root at all, so `homonto update` is
unavailable in it. That is the safe default, not a defect;
`homonto update trust` says so plainly.

## After publishing

Verify the published release the way a user's binary will:

```sh
curl -fsSLO <release-url>/release-manifest.json
curl -fsSLO <release-url>/SHA256SUMS
curl -fsSLO <release-url>/homonto_<version>_linux_amd64.tar.gz
sha256sum -c --ignore-missing SHA256SUMS
```

and then, on a machine running the previous release, an actual
`homonto update`. The rehearsal proves the path works; this proves the
published bytes are the ones that were rehearsed.
