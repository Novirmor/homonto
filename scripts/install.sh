#!/usr/bin/env bash
# Interactive installer for homonto, plus the onto and to workflow binaries.
# Asks a few questions, downloads the release archives from GitHub, verifies
# them against the release SHA256SUMS, and installs the binaries into a
# directory you choose — and, on your explicit confirmation only, runs
# `homonto init` in the current directory to scaffold homonto.toml and offers
# to configure workflow frameworks, models, records, and sibling repositories.
#
# Prompts use gum (https://github.com/charmbracelet/gum) when it is on PATH and
# stdin is a TTY, then dialog, then plain reads. Scripted runs keep
# working. The installer never edits your shell configuration —
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
SETUP_RAN=0
SETUP_FRAMEWORKS="none"
WORKFLOW_ROOT="docs"
WORKFLOW_MODEL=""
REPO_NAMES=()
REPO_PATHS=()
UI_SECTION="Install homonto"

usage() {
  cat <<'EOF'
usage: scripts/install.sh [--help]

Asks which binaries to install, downloads and verifies the release archives
against SHA256SUMS, installs into your chosen directory, and prints PATH
instructions. On explicit confirmation it also runs `homonto init` in the
current directory, then configures only the newly created homonto.toml. It
never edits your shell configuration or an existing config. Linux and macOS
(amd64 and arm64) only. Prompts use gum, then dialog, when available and stdin
is a TTY; installing either is optional.
EOF
}

die() { printf 'install: %s\n' "$*" >&2; exit 1; }

# --- prompt layer: gum when interactive, plain reads otherwise -------------

ui_mode() { # echoes dialog|gum|plain; HOMONTO_UI forces it (tests use it)
  case "${HOMONTO_UI:-auto}" in
    dialog) echo dialog ;;
    gum)   echo gum ;;
    plain) echo plain ;;
    auto)
      if [ -t 0 ] && command -v gum >/dev/null 2>&1; then echo gum
      elif [ -t 0 ] && command -v dialog >/dev/null 2>&1; then echo dialog
      else echo plain
      fi
      ;;
    *) die "HOMONTO_UI must be auto, dialog, gum, or plain" ;;
  esac
}

ensure_ui() {
  case "${HOMONTO_UI:-auto}" in
    dialog) command -v dialog >/dev/null 2>&1 || die "HOMONTO_UI=dialog requires dialog on PATH (or use HOMONTO_UI=plain)" ;;
    gum) command -v gum >/dev/null 2>&1 || die "HOMONTO_UI=gum requires gum on PATH (or use HOMONTO_UI=plain)" ;;
  esac
}

ui_heading() {
  if [ "$(ui_mode)" = gum ]; then
    gum style --border rounded --padding "0 1" --bold --foreground 212 \
      "homonto installer" "Verified binaries. Your shell files stay untouched." >&2
  elif [ "$(ui_mode)" = dialog ]; then
    dialog --backtitle "homonto installer" --title "$UI_SECTION" --infobox \
      "Verified binaries. Shell setup stays yours." 5 56
  else
    printf '\n+------------------------------------------------+\n' >&2
    printf '| homonto installer                              |\n' >&2
    printf '| Verified binaries. Shell setup stays yours.   |\n' >&2
    printf '+------------------------------------------------+\n' >&2
  fi
}

ui_section() {
  UI_SECTION="$1"
  if [ "$(ui_mode)" = gum ]; then
    gum style --bold --foreground 212 "$1" >&2
  elif [ "$(ui_mode)" = dialog ]; then
    :
  else
    printf '\n[ %s ]\n' "$1" >&2
  fi
}

ui_hint() { printf '  > %s\n' "$1" >&2; }

# ask <prompt> <default> -> echoes the answer (empty when the default is used).
# Prompt goes to stderr so the answer is the only thing on stdout, and callers
# capture it. EOF (closed stdin) means "use the default".
ask() {
  local prompt="$1" default="$2" answer=""
  printf '  ? %s' "$prompt" >&2
  [ -n "$default" ] && printf ' [%s]' "$default" >&2
  printf ' ' >&2
  if ! IFS= read -r answer; then answer=""; fi
  if [ -n "$answer" ]; then printf '%s\n' "$answer"; else printf '%s\n' "$default"; fi
}

# ui_input <prompt> <default> -> the answer (default when empty/cancelled).
ui_input() {
  local prompt="$1" default="$2" value=""
  if [ "$(ui_mode)" = dialog ]; then
    if ! value="$(dialog --clear --stdout --backtitle "homonto installer" --title "$UI_SECTION" \
      --inputbox "$prompt" 8 72 "$default")"; then
      die "aborted at: ${prompt}"
    fi
    [ -n "$value" ] || value="$default"
    printf '%s\n' "$value"
  elif [ "$(ui_mode)" = gum ]; then
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
  if [ "$(ui_mode)" = dialog ]; then
    local choice="" option menu=()
    for option in "$@"; do menu+=("$option" "$option"); done
    if ! choice="$(dialog --clear --stdout --backtitle "homonto installer" --title "$UI_SECTION" \
      --default-item "$1" --menu "$prompt" 15 72 6 "${menu[@]}")"; then
      die "aborted at: ${prompt}"
    fi
    printf '%s\n' "$choice"
  elif [ "$(ui_mode)" = gum ]; then
    local choice=""
    if ! choice="$(gum choose --header "$prompt" "$@")"; then
      die "aborted at: ${prompt}"
    fi
    printf '%s\n' "$choice"
  else
    local input o i
    printf '  ? %s\n' "$prompt" >&2
    i=1
    for o in "$@"; do
      if [ "$o" = "$1" ]; then
        printf '    %d) %s (default)\n' "$i" "$o" >&2
      else
        printf '    %d) %s\n' "$i" "$o" >&2
      fi
      i=$((i + 1))
    done
    while :; do
      input="$(ask "Choose" "$1")"
      i=1
      for o in "$@"; do
        if [ "$input" = "$i" ]; then printf '%s\n' "$o"; return 0; fi
        if [ "$input" = "$o" ]; then printf '%s\n' "$input"; return 0; fi
        i=$((i + 1))
      done
      printf 'install: choose one of: %s\n' "$*" >&2
    done
  fi
}

# ui_confirm <prompt> [yes|no] -> exit 0 on yes, 1 on no. Default no unless
# the second argument is "yes".
ui_confirm() {
  local prompt="$1" default="${2:-no}"
  if [ "$(ui_mode)" = dialog ]; then
    if [ "$default" = yes ]; then
      dialog --clear --backtitle "homonto installer" --title "$UI_SECTION" --yesno "$prompt" 8 72
    else
      dialog --clear --backtitle "homonto installer" --title "$UI_SECTION" --defaultno --yesno "$prompt" 8 72
    fi
  elif [ "$(ui_mode)" = gum ]; then
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
  local config_existed=0
  [ -e homonto.toml ] && config_existed=1
  ui_hint "Optional: scaffold this directory now. Existing homonto.toml files are never overwritten."
  if ui_confirm "Generate homonto.toml here (runs homonto init in $(pwd))?"; then
    printf 'running homonto init in %s\n' "$(pwd)" >&2
    "$INSTALL_DIR/homonto" init >&2
    INIT_RAN=1
    if [ "$config_existed" -eq 0 ] && [ -f homonto.toml ]; then
      configure_new_project
    elif [ "$config_existed" -eq 1 ]; then
      ui_hint "homonto.toml already exists, so its configuration was left unchanged."
    fi
  fi
}

default_workflow_model() {
  local config model
  for config in "$PWD/.opencode/opencode.json" "$PWD/.opencode/opencode.jsonc" \
    "${XDG_CONFIG_HOME:-$HOME/.config}/opencode/opencode.json" \
    "${XDG_CONFIG_HOME:-$HOME/.config}/opencode/opencode.jsonc"; do
    [ -r "$config" ] || continue
    model="$(sed -n 's/^[[:space:]]*"model"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$config" | head -n 1)"
    [ -n "$model" ] && { printf '%s\n' "$model"; return 0; }
  done
  printf '%s\n' 'opencode-go/qwen3.7-plus'
}

safe_toml_value() {
  case "$1" in
    *'"'* | *$'\r'* | *$'\n'*) die "configuration values cannot contain quotes or newlines" ;;
  esac
}

ask_setup_frameworks() {
  case "$WORKFLOW_BIN" in
    both) SETUP_FRAMEWORKS="$(ui_select "Frameworks to configure" both onto to none)" ;;
    onto) SETUP_FRAMEWORKS="$(ui_select "Frameworks to configure" onto both to none)" ;;
    to)   SETUP_FRAMEWORKS="$(ui_select "Frameworks to configure" to both onto none)" ;;
    *)    SETUP_FRAMEWORKS="$(ui_select "Frameworks to configure" none both onto to)" ;;
  esac
}

ask_workflow_root() {
  while :; do
    WORKFLOW_ROOT="$(ui_input "Workflow records directory" "docs")"
    safe_toml_value "$WORKFLOW_ROOT"
    case "$WORKFLOW_ROOT" in
      '' | . | /* | .. | ../* | */../* | *\\*)
        printf 'install: workflow records directory must be a relative path below this repository\n' >&2
        ;;
      *) return 0 ;;
    esac
  done
}

repo_name_seen() {
  local name="$1" existing
  for existing in "${REPO_NAMES[@]}"; do
    [ "$existing" = "$name" ] && return 0
  done
  return 1
}

collect_repositories() {
  local path name
  ui_hint "The current directory is the config repository and is already included."
  ui_hint "Add sibling Git repositories one at a time; press Enter with no path when finished."
  while :; do
    path="$(ui_input "Additional repository path" "")"
    [ -n "$path" ] || return 0
    safe_toml_value "$path"
    [ -d "$path" ] || { printf 'install: repository path does not exist: %s\n' "$path" >&2; continue; }
    command -v git >/dev/null 2>&1 || die "git is required to add a repository"
    git -C "$path" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
      || { printf 'install: repository path is not a Git worktree: %s\n' "$path" >&2; continue; }
    name="$(ui_input "Repository name" "$(basename "$path")")"
    if ! [[ "$name" =~ ^[A-Za-z0-9_-]+$ ]]; then
      printf 'install: repository name must use letters, numbers, hyphens, or underscores\n' >&2
      continue
    fi
    if repo_name_seen "$name"; then
      printf 'install: repository name already selected: %s\n' "$name" >&2
      continue
    fi
    REPO_NAMES+=("$name")
    REPO_PATHS+=("$path")
    ui_hint "Added repository $name -> $path"
  done
}

write_framework_config() {
  local framework name
  local names=()
  for framework in "$@"; do
    printf '\n[frameworks.%s]\nsource = "builtin:%s"\nscope = "project"\n' "$framework" "$framework"
    case "$framework" in
      onto) names=(onto onto-explorer onto-reviewer onto-implementer onto-skeptic) ;;
      to) names=(to to-explorer to-reviewer to-implementer to-skeptic) ;;
    esac
    for name in "${names[@]}"; do
      printf '\n[subagents.%s.opencode]\nmodel = "%s"\n' "$name" "$WORKFLOW_MODEL"
    done
  done
}

configure_new_project() {
  local i frameworks=()
  ui_section "Configure project"
  ui_hint "Choose the workflow configuration for this new repository."
  ask_setup_frameworks
  ask_workflow_root
  if [ "$SETUP_FRAMEWORKS" != none ]; then
    WORKFLOW_MODEL="$(ui_input "Model for OpenCode and all workflow agents" "$(default_workflow_model)")"
    safe_toml_value "$WORKFLOW_MODEL"
    [ -n "$WORKFLOW_MODEL" ] || die "a workflow model is required when enabling a framework"
  fi
  collect_repositories

  case "$SETUP_FRAMEWORKS" in
    both) frameworks=(onto to) ;;
    onto) frameworks=(onto) ;;
    to) frameworks=(to) ;;
  esac
  {
    printf '\n# Generated by scripts/install.sh. Adjust these values as your project evolves.\n'
    printf '\n[workflow]\nroot = "%s"\n' "$WORKFLOW_ROOT"
    if [ "${#REPO_NAMES[@]}" -gt 0 ]; then
      printf '\n[repos]\n'
      for i in "${!REPO_NAMES[@]}"; do
        printf '%s = "%s"\n' "${REPO_NAMES[$i]}" "${REPO_PATHS[$i]}"
      done
    fi
    if [ "${#frameworks[@]}" -gt 0 ]; then
      printf '\n[settings.opencode]\nmodel = "%s"\n' "$WORKFLOW_MODEL"
      write_framework_config "${frameworks[@]}"
    fi
  } >> homonto.toml
  SETUP_RAN=1
  ui_hint "Configured homonto.toml. Review it before applying changes."
}

next_steps() {
  printf '\nInstalled\n' >&2
  "$INSTALL_DIR/homonto" version >&2
  local bin
  for bin in $(binaries_to_install); do
    [ "$bin" = homonto ] && continue
    "$INSTALL_DIR/$bin" version >&2
  done
  if [ "$SETUP_RAN" -eq 1 ]; then
    printf '\nNext steps\n' >&2
    printf '  Review homonto.toml, then run homonto plan and homonto apply.\n' >&2
    printf '  onto and to are complementary: choose either primary per change.\n' >&2
  elif [ "$INIT_RAN" -eq 1 ]; then
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
