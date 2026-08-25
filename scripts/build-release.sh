#!/usr/bin/env bash
# Cross-compile, package, checksum, and describe the homonto binary for
# every supported target.
#
# One binary, four targets. Linux and macOS on amd64 and arm64 are the
# platforms the workflows are actually exercised on; nothing else is
# published, because an archive for a platform nobody ran is a promise
# nobody checked.
#
# Usage:
#   scripts/build-release.sh [options] <version>
#     --list                 print the asset names and exit, building nothing
#     --dist <dir>           output directory (default: dist)
#     --targets "os/arch …"  override the target set
#     --base-url <url>       https prefix the archives will be published under
#
# Signing is deliberately NOT here. This script produces the unsigned
# manifest; scripts/release-guard.sh decides whether a release may happen,
# and tools/release-sign adds the signature. Keeping them apart means a
# local packaging rehearsal can never touch a signing key.
set -euo pipefail
cd "$(dirname "$0")/.."

TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
DIST="dist"
BASE_URL="https://github.com/noviopenworks/homonto/releases/download"
LIST_ONLY=""
VERSION=""

while [ $# -gt 0 ]; do
  case "$1" in
    --list) LIST_ONLY=1; shift ;;
    --dist) DIST="${2:?--dist needs a directory}"; shift 2 ;;
    --targets) TARGETS="${2:?--targets needs a list}"; shift 2 ;;
    --base-url) BASE_URL="${2:?--base-url needs a url}"; shift 2 ;;
    -*) echo "build-release: unknown option $1" >&2; exit 2 ;;
    *) VERSION="$1"; shift ;;
  esac
done

[ -n "$VERSION" ] || { echo "usage: build-release.sh [options] <version>" >&2; exit 2; }

# A '/' in the version would nest archives under $DIST/<...>/ and the flat
# globs below would silently miss them, yielding an incomplete release.
case "$VERSION" in
  */*) echo "build-release: version must not contain '/': $VERSION" >&2; exit 1 ;;
esac

archive_name() { echo "homonto_${VERSION}_${1}_${2}.tar.gz"; }

if [ -n "$LIST_ONLY" ]; then
  for target in $TARGETS; do
    archive_name "${target%/*}" "${target#*/}"
  done
  echo "SHA256SUMS"
  echo "release-manifest.json"
  exit 0
fi

mkdir -p "$DIST"

for target in $TARGETS; do
  goos="${target%/*}"
  goarch="${target#*/}"
  stage="homonto_${VERSION}_${goos}_${goarch}"
  echo "building $stage"
  mkdir -p "$DIST/$stage"
  cp LICENSE README.md "$DIST/$stage/"
  # -trimpath so the archive does not embed the build machine's paths, and
  # CGO off so a Linux archive runs on any Linux rather than the one whose
  # libc happened to be present at build time.
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath \
      -ldflags "-s -w -X github.com/noviopenworks/homonto/internal/cli.Version=${VERSION}" \
      -o "$DIST/$stage/homonto" .
  tar -C "$DIST" -czf "$DIST/$stage.tar.gz" "$stage"
  rm -rf "${DIST:?}/$stage"
done

# The manifest is generated from the archives on disk, by a tool that uses
# the same types the shipped binary parses it with.
go run ./tools/release-manifest \
  --version "$VERSION" --dist "$DIST" --base-url "$BASE_URL/$VERSION" \
  --out "$DIST/release-manifest.json"

# sha256sum is a GNU coreutils tool the macOS runners do not carry; shasum
# prints the same "digest  path" lines. Output format must stay identical
# either way — downloaders check SHA256SUMS against what the other tool
# wrote.
sums="sha256sum"
command -v sha256sum >/dev/null 2>&1 || sums="shasum -a 256"
( cd "$DIST" && $sums ./homonto_*.tar.gz > SHA256SUMS && cat SHA256SUMS )
