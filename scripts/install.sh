#!/usr/bin/env bash
# Interactive installer for homonto, plus optionally one workflow binary
# (onto or to). Asks a few questions on stdin, downloads the release archives
# from GitHub, verifies them against the release SHA256SUMS, and installs the
# binaries into a directory you choose.
#
# The installer never edits your shell configuration — PATH setup is printed
# for you to apply. It never runs project commands (homonto init, apply, ...):
# installing is all it does. With no stdin (e.g. `scripts/install.sh </dev/null`)
# every question falls back to its default: latest release, onto, ~/.local/bin.
#
# Usage: scripts/install.sh [--help]
set -euo pipefail

REPO="noviopenworks/homonto"
BASE_URL="https://github.com/${REPO}/releases/download"
API_LATEST="https://api.github.com/repos/${REPO}/releases/latest"

VERSION=""            # empty -> resolved from the GitHub API
WORKFLOW_BIN="onto"   # onto | to | none
INSTALL_DIR="${HOME}/.local/bin"

usage() {
  cat <<'EOF'
usage: scripts/install.sh [--help]

Asks which binaries to install, downloads and verifies the release archives
against SHA256SUMS, installs into your chosen directory, and prints PATH
instructions. It never edits your shell configuration and never runs project
commands. Linux and macOS (amd64 and arm64) only.
EOF
}

die() { printf 'install: %s\n' "$*" >&2; exit 1; }

# ask <prompt> <default> -> echoes the answer (empty when the default is used).
# Prompt goes to stderr so the answer is the only thing on stdout, and callers
# capture it. EOF (closed stdin) means "use the default".
ask() {
  local prompt="$1" default="$2" answer=""
  printf '%s' "$prompt" >&2
  [ -n "$default" ] && printf ' [%s]' "$default" >&2
  printf ' ' >&2
  if ! IFS= read -r answer; then answer=""; fi
  if [ -n "$answer" ]; then printf '%s\n' "$answer"; else printf '%s\n' "$default"; fi
}

detect_platform() {
  local os arch
  os="$(uname -s)"
  case "$os" in
    Linux)  GOOS="linux" ;;
    Darwin) GOOS="darwin" ;;
    *) die "unsupported operating system: ${os} (Linux and macOS only; Windows installs are manual ZIP extraction)" ;;
  esac
  arch="$(uname -m)"
  case "$arch" in
    x86_64 | amd64)          GOARCH="amd64" ;;
    aarch64 | arm64)         GOARCH="arm64" ;;
    *) die "unsupported architecture: ${arch} (amd64 and arm64 only)" ;;
  esac
  printf 'platform: %s/%s\n' "$GOOS" "$GOARCH" >&2
}

SUM_TOOL=() # checksum command as an argv array: sha256sum, or shasum -a 256

pick_sum_tool() {
  # HOMONTO_SUM overrides the checksum tool; the mocked install tests use it to
  # force the shasum fallback path without hiding the real sha256sum.
  if [ -n "${HOMONTO_SUM:-}" ]; then
    read -r -a SUM_TOOL <<<"$HOMONTO_SUM"
  elif command -v sha256sum >/dev/null 2>&1; then
    SUM_TOOL=(sha256sum)
  elif command -v shasum >/dev/null 2>&1; then
    SUM_TOOL=(shasum -a 256)
  else
    die "neither sha256sum nor shasum is available; cannot verify downloads"
  fi
}

resolve_latest() {
  curl -fsSL "$API_LATEST" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

normalize_version() { # 1.2.3 -> v1.2.3 ; v1.2.3 -> v1.2.3
  case "$1" in v*) printf '%s\n' "$1" ;; *) printf 'v%s\n' "$1" ;; esac
}

ask_version() {
  local latest input
  if [ -z "$VERSION" ]; then
    latest="$(resolve_latest)" || die "could not look up the latest release (network or API failure)"
    [ -n "$latest" ] || die "could not parse the latest release from the GitHub API"
  else
    latest="$VERSION"
  fi
  latest="$(normalize_version "$latest")"
  while :; do
    input="$(normalize_version "$(ask "Install version" "$latest")")"
    if [[ "$input" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
      VERSION="$input"
      return 0
    fi
    printf 'install: "%s" is not a version like v0.17.0\n' "$input" >&2
  done
}

ask_workflow_bin() {
  local input
  while :; do
    input="$(ask "Also install a workflow binary (onto|to|none)" "$WORKFLOW_BIN")"
    case "$input" in
      onto | to | none) WORKFLOW_BIN="$input"; return 0 ;;
      *) printf 'install: choose onto, to, or none\n' >&2 ;;
    esac
  done
}

ask_install_dir() {
  INSTALL_DIR="$(ask "Install directory" "$INSTALL_DIR")"
  case "$INSTALL_DIR" in
    /*) ;;
    *) die "install directory must be an absolute path: ${INSTALL_DIR}" ;;
  esac
  mkdir -p "$INSTALL_DIR" 2>/dev/null || die "cannot create install directory: ${INSTALL_DIR}"
}

binaries_to_install() {
  printf '%s\n' homonto
  case "$WORKFLOW_BIN" in
    onto | to) printf '%s\n' "$WORKFLOW_BIN" ;;
  esac
}

asset_name() { # <binary> -> homonto_v0.17.0_linux_amd64.tar.gz
  printf '%s_%s_%s_%s.tar.gz\n' "$1" "$VERSION" "$GOOS" "$GOARCH"
}

verify_asset() { # <asset> <workdir>
  local asset="$1" dir="$2" line digest
  # Release manifests write "digest  ./name_...tar.gz" (sha256sum of globs
  # keeps the ./); match the exact last field so "onto_..." cannot match the
  # line of "homonto_...", and rebuild the line against the local file.
  line="$(awk -v a="$asset" '$NF == a || $NF == "./" a { print; exit }' "$dir/SHA256SUMS")"
  [ -n "$line" ] || die "SHA256SUMS has no entry for ${asset}"
  digest="${line%% *}"
  (cd "$dir" && "${SUM_TOOL[@]}" -c <<<"$digest  $asset") >/dev/null 2>&1 \
    || die "checksum mismatch for ${asset} — the download is not the official archive"
}

install_binary() { # <binary> <workdir>
  local bin="$1" workdir="$2" asset member staging target
  asset="$(asset_name "$bin")"
  member="${bin}_${VERSION}_${GOOS}_${GOARCH}/${bin}"
  staging="$workdir/stage-$bin"
  mkdir -p "$staging"
  # Extract only the single executable member of the archive.
  tar -xzf "$workdir/$asset" -C "$staging" --strip-components=1 "$member"
  target="$INSTALL_DIR/$bin"
  if [ -e "$target" ]; then
    case "$(ask "$bin already exists at ${target} — overwrite? (y/N)" "n")" in
      y | Y | yes | YES) ;;
      *) die "aborted: ${bin} already exists at ${target} and was not replaced" ;;
    esac
  fi
  # Two moves: cross-filesystem copy first, then an atomic same-filesystem rename.
  mv -f "$staging/$bin" "$target.tmp.$$"
  mv -f "$target.tmp.$$" "$target"
  chmod 0755 "$target"
  printf 'installed %s -> %s\n' "$bin" "$target" >&2
}

path_advice() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*)
      printf '%s is already on PATH.\n' "$INSTALL_DIR" >&2
      ;;
    *)
      printf 'PATH:\n' >&2
      printf '  The installer never edits your shell configuration. To use the\n' >&2
      printf '  binaries, add this line to your shell profile (~/.bashrc, ~/.zshrc,\n' >&2
      printf '  or ~/.profile), or run it in this session:\n\n' >&2
      # shellcheck disable=SC2016 # the literal $PATH is what the user must paste
      printf '    export PATH="%s:$PATH"\n\n' "$INSTALL_DIR" >&2
      ;;
  esac
}

next_steps() {
  printf 'installed versions:\n' >&2
  "$INSTALL_DIR/homonto" version >&2
  case "$WORKFLOW_BIN" in
    onto | to) "$INSTALL_DIR/$WORKFLOW_BIN" version >&2 ;;
  esac
  printf 'next: in the directory that should hold homonto.toml, run homonto init,\n' >&2
  printf 'edit homonto.toml, then homonto plan and homonto apply.\n' >&2
}

WORKDIR="" # global so the EXIT trap can clean it up after main unwinds

main() {
  case "${1:-}" in
    -h | --help) usage; exit 0 ;;
    *) [ $# -eq 0 ] || die "unknown argument: $1" ;;
  esac
  command -v curl >/dev/null 2>&1 || die "curl is required"
  detect_platform
  pick_sum_tool
  ask_version
  ask_workflow_bin
  ask_install_dir
  WORKDIR="$(mktemp -d)"
  trap 'rm -rf "$WORKDIR"' EXIT
  local bin asset
  for bin in $(binaries_to_install); do
    asset="$(asset_name "$bin")"
    printf 'downloading %s\n' "$asset" >&2
    curl -fsSL -o "$WORKDIR/$asset" "$BASE_URL/$VERSION/$asset"
  done
  printf 'downloading SHA256SUMS\n' >&2
  curl -fsSL -o "$WORKDIR/SHA256SUMS" "$BASE_URL/$VERSION/SHA256SUMS"
  for bin in $(binaries_to_install); do
    verify_asset "$(asset_name "$bin")" "$WORKDIR"
  done
  for bin in $(binaries_to_install); do
    install_binary "$bin" "$WORKDIR"
  done
  path_advice
  next_steps
}

main "$@"