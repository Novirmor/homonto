#!/usr/bin/env bash
# Interactive installer for homonto, plus the onto and to workflow binaries.
# Asks a few questions, downloads the release archives from GitHub, verifies
# them against the release SHA256SUMS, and installs the binaries into a
# directory you choose — and, on your explicit confirmation only, runs
# `homonto init` in the current directory to scaffold homonto.toml.
#
# Prompts use gum (https://github.com/charmbracelet/gum) when it is on PATH
# and stdin is a TTY; otherwise they fall back to plain reads, so scripted
# runs keep working. The installer never edits your shell configuration —
# PATH setup is printed for you to apply. With no stdin (e.g.
# `scripts/install.sh </dev/null`) every question falls back to its default:
# latest release, both workflow binaries, ~/.local/bin, no init.
#
# Usage: scripts/install.sh [--help]
set -euo pipefail

REPO="noviopenworks/homonto"
BASE_URL="https://github.com/${REPO}/releases/download"
API_LATEST="https://api.github.com/repos/${REPO}/releases/latest"

VERSION=""            # empty -> resolved from the GitHub API
WORKFLOW_BIN="both"   # both | onto | to | none
INSTALL_DIR="${HOME}/.local/bin"
INIT_RAN=0

usage() {
  cat <<'EOF'
usage: scripts/install.sh [--help]

Asks which binaries to install, downloads and verifies the release archives
against SHA256SUMS, installs into your chosen directory, and prints PATH
instructions. On explicit confirmation it also runs `homonto init` in the
current directory. It never edits your shell configuration. Linux and macOS
(amd64 and arm64) only. Prompts use gum when available and stdin is a TTY;
installing gum is optional.
EOF
}

die() { printf 'install: %s\n' "$*" >&2; exit 1; }

# --- prompt layer: gum when interactive, plain reads otherwise -------------

ui_mode() { # echoes gum|plain; HOMONTO_UI forces it (tests use it)
  case "${HOMONTO_UI:-auto}" in
    gum)   echo gum ;;
    plain) echo plain ;;
    auto) if [ -t 0 ] && command -v gum >/dev/null 2>&1; then echo gum; else echo plain; fi ;;
    *) die "HOMONTO_UI must be auto, gum, or plain" ;;
  esac
}

ensure_ui() {
  if [ "${HOMONTO_UI:-auto}" = gum ] && ! command -v gum >/dev/null 2>&1; then
    die "HOMONTO_UI=gum requires gum on PATH (or use HOMONTO_UI=plain)"
  fi
}

ui_heading() {
  if [ "$(ui_mode)" = gum ]; then
    gum style --border rounded --padding "0 1" --bold --foreground 212 \
      "homonto installer" "Verified binaries. Your shell files stay untouched." >&2
  else
    printf '\n== homonto installer ==\nVerified binaries. Your shell files stay untouched.\n' >&2
  fi
}

ui_section() {
  if [ "$(ui_mode)" = gum ]; then
    gum style --bold --foreground 212 "$1" >&2
  else
    printf '\n-- %s --\n' "$1" >&2
  fi
}

ui_hint() { printf '  %s\n' "$1" >&2; }

# ask <prompt> <default> -> echoes the answer (empty when the default is used).
# Prompt goes to stderr so the answer is the only thing on stdout, and callers
# capture it. EOF (closed stdin) means "use the default".
ask() {
  local prompt="$1" default="$2" answer=""
  printf '  %s' "$prompt" >&2
  [ -n "$default" ] && printf ' [%s]' "$default" >&2
  printf ' ' >&2
  if ! IFS= read -r answer; then answer=""; fi
  if [ -n "$answer" ]; then printf '%s\n' "$answer"; else printf '%s\n' "$default"; fi
}

# ui_input <prompt> <default> -> the answer (default when empty/cancelled).
ui_input() {
  local prompt="$1" default="$2" value=""
  if [ "$(ui_mode)" = gum ]; then
    if ! value="$(gum input --header "$prompt" --placeholder "$default")"; then
      die "aborted at: ${prompt}"
    fi
    [ -n "$value" ] || value="$default"
    printf '%s\n' "$value"
  else
    ask "$prompt" "$default"
  fi
}

# ui_select <prompt> <option...> -> the chosen option. The FIRST option is
# the default (gum highlights it; plain mode shows it in brackets).
ui_select() {
  local prompt="$1"; shift
  if [ "$(ui_mode)" = gum ]; then
    local choice=""
    if ! choice="$(gum choose --header "$prompt" "$@")"; then
      die "aborted at: ${prompt}"
    fi
    printf '%s\n' "$choice"
  else
    local input
    while :; do
      input="$(ask "$prompt" "$1")"
      local o
      for o in "$@"; do
        if [ "$input" = "$o" ]; then printf '%s\n' "$input"; return 0; fi
      done
      printf 'install: choose one of: %s\n' "$*" >&2
    done
  fi
}

# ui_confirm <prompt> [yes|no] -> exit 0 on yes, 1 on no. Default no unless
# the second argument is "yes".
ui_confirm() {
  local prompt="$1" default="${2:-no}"
  if [ "$(ui_mode)" = gum ]; then
    if [ "$default" = yes ]; then
      gum confirm "$prompt"
    else
      gum confirm --default=false "$prompt"
    fi
  else
    case "$(ask "$prompt (y/n)" "$default")" in
      y | Y | yes | YES) return 0 ;;
      *) return 1 ;;
    esac
  fi
}

# --- install steps ----------------------------------------------------------

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
    input="$(normalize_version "$(ui_input "Install version" "$latest")")"
    if [[ "$input" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
      VERSION="$input"
      return 0
    fi
    printf 'install: "%s" is not a version like v0.18.0\n' "$input" >&2
  done
}

ask_workflow_bin() {
  ui_hint "both (recommended): onto for gated work, to for lightweight work."
  ui_hint "Choose one only when you know you need a single workflow."
  WORKFLOW_BIN="$(ui_select "Workflow binaries" both onto to none)"
}

ask_install_dir() {
  INSTALL_DIR="$(ui_input "Install directory" "$INSTALL_DIR")"
  case "$INSTALL_DIR" in
    /*) ;;
    *) die "install directory must be an absolute path: ${INSTALL_DIR}" ;;
  esac
  mkdir -p "$INSTALL_DIR" 2>/dev/null || die "cannot create install directory: ${INSTALL_DIR}"
}

binaries_to_install() {
  printf '%s\n' homonto
  case "$WORKFLOW_BIN" in
    onto) printf '%s\n' onto ;;
    to)   printf '%s\n' to ;;
    both) printf '%s\n' onto; printf '%s\n' to ;;
  esac
}

asset_name() { # <binary> -> homonto_v0.18.0_linux_amd64.tar.gz
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
    if ! ui_confirm "$bin already exists at ${target} — overwrite?"; then
      die "aborted: ${bin} already exists at ${target} and was not replaced"
    fi
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

maybe_init() {
  # homonto init never overwrites an existing homonto.toml, so this is safe
  # to accept in a directory that is already configured.
  ui_hint "Optional: scaffold this directory now. Existing homonto.toml files are never overwritten."
  if ui_confirm "Generate homonto.toml here (runs homonto init in $(pwd))?"; then
    printf 'running homonto init in %s\n' "$(pwd)" >&2
    "$INSTALL_DIR/homonto" init >&2
    INIT_RAN=1
  fi
}

next_steps() {
  printf '\nInstalled\n' >&2
  "$INSTALL_DIR/homonto" version >&2
  local bin
  for bin in $(binaries_to_install); do
    [ "$bin" = homonto ] && continue
    "$INSTALL_DIR/$bin" version >&2
  done
  if [ "$INIT_RAN" -eq 1 ]; then
    printf '\nNext steps\n' >&2
    printf '  Edit homonto.toml (declare MCPs / skills / frameworks), then\n' >&2
    printf 'homonto plan and homonto apply. onto and to are complementary —\n' >&2
    printf 'declare either or both, pick per change by selecting its agent.\n' >&2
  else
    printf '\nNext steps\n' >&2
    printf '  In the directory that should hold homonto.toml, run homonto init,\n' >&2
    printf 'edit homonto.toml, then homonto plan and homonto apply. onto and to are\n' >&2
    printf 'complementary — declare either or both, pick per change.\n' >&2
  fi
}

WORKDIR="" # global so the EXIT trap can clean it up after main unwinds

main() {
  case "${1:-}" in
    -h | --help) usage; exit 0 ;;
    *) [ $# -eq 0 ] || die "unknown argument: $1" ;;
  esac
  ensure_ui
  ui_heading
  command -v curl >/dev/null 2>&1 || die "curl is required"
  detect_platform
  pick_sum_tool
  ui_section "Release"
  ask_version
  ui_section "Workflow"
  ask_workflow_bin
  ui_section "Destination"
  ask_install_dir
  WORKDIR="$(mktemp -d)"
  trap 'rm -rf "$WORKDIR"' EXIT
  ui_section "Download and verify"
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
  ui_hint "SHA256SUMS verified."
  ui_section "Install"
  for bin in $(binaries_to_install); do
    install_binary "$bin" "$WORKDIR"
  done
  path_advice
  ui_section "Project setup"
  maybe_init
  next_steps
}

main "$@"
