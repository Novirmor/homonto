#!/usr/bin/env bash
# Cross-compile, package, and checksum the homonto, onto, and to binaries for
# every supported target, producing one archive per binary per target plus a
# single SHA256SUMS. Runnable locally (Go cross-compiles from any host); the
# release workflow calls it so the same code path runs on and off CI.
#
# Usage: scripts/build-release.sh <version>
set -eu

VERSION="${1:?usage: build-release.sh <version>}"

# A '/' in the version would nest archives under dist/<...>/ and the flat
# dist/*.tar.gz / dist/*.zip globs (SHA256SUMS + the release publish step) would
# silently miss them, yielding an incomplete release. Reject it up front.
case "$VERSION" in
  */*) echo "build-release: version must not contain '/': $VERSION" >&2; exit 1 ;;
esac

# Six targets: macOS ships amd64+arm64; Linux and Windows cover both too.
TARGETS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

# build_one packages a single binary for one target. The binaries differ
# only by name / build path / version package, so this is the sole build
# definition — no copy-pasted loop body.
#   build_one <name> <buildpath> <versionpkg> <goos> <goarch>
build_one() {
  name="$1"; buildpath="$2"; versionpkg="$3"; goos="$4"; goarch="$5"
  bin="$name"
  [ "$goos" = "windows" ] && bin="$name.exe"
  out="${name}_${VERSION}_${goos}_${goarch}"
  echo "building $out"
  mkdir -p "dist/$out"
  cp LICENSE README.md "dist/$out/"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w -X ${versionpkg}.Version=${VERSION}" \
      -o "dist/$out/$bin" "$buildpath"
  if [ "$goos" = "windows" ]; then
    (cd dist && zip -qr "$out.zip" "$out")
  else
    tar -C dist -czf "dist/$out.tar.gz" "$out"
  fi
  rm -rf "dist/$out"
}

mkdir -p dist
for target in $TARGETS; do
  goos="${target%/*}"
  goarch="${target#*/}"
  build_one homonto . github.com/noviopenworks/homonto/internal/cli "$goos" "$goarch"
  build_one onto ./cmd/onto github.com/noviopenworks/homonto/internal/ontocli "$goos" "$goarch"
  build_one to ./cmd/to github.com/noviopenworks/homonto/internal/tocli "$goos" "$goarch"
done

cd dist
# One SHA256SUMS over every archive, so downloaders can verify either binary.
# sha256sum is GNU coreutils, absent on macOS runners; shasum prints the same
# "digest  path" lines. The output format must stay identical either way —
# downloaders check SHA256SUMS against what the other tool wrote.
sums="sha256sum"
command -v sha256sum >/dev/null 2>&1 || sums="shasum -a 256"
$sums ./*.tar.gz ./*.zip > SHA256SUMS
cat SHA256SUMS
