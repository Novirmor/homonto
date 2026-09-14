#!/usr/bin/env bash
# Mocked-network tests for scripts/install.sh.
#
# Every test runs the installer against a fake $PATH whose curl serves canned
# release archives from a scratch directory and whose uname pins the platform,
# so nothing touches the network or the real filesystem outside the scratch
# dir. Answers are piped on stdin; the installer contract — all output to
# stderr, PATH advice printed not applied, checksum verification enforced — is
# asserted per test. Runs offline; wired into scripts/gate.sh.
# Invoke this suite with Bash 3.2 to check the installer's empty-array compatibility.
set -uo pipefail

ROOT="$(CDPATH='' cd "$(dirname "$0")/.." && pwd)"
INSTALLER="$ROOT/scripts/install.sh"

PASS=0
FAIL=0
SUMMARY=()

ok()  { PASS=$((PASS + 1)); SUMMARY+=("ok   $1"); }
bad() { FAIL=$((FAIL + 1)); SUMMARY+=("FAIL $1"); }

# --- mock tooling ----------------------------------------------------------

make_mocks() { # <dir> -> curl, uname, and shasum mocks
  local d="$1"
  cat >"$d/curl" <<'EOF'
#!/usr/bin/env bash
# mock curl: -fsSL <url> [-o <out>] — serves canned releases from $MOCK_ASSETS.
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -f|-s|-S|-L) shift ;;
    *) url="$1"; shift ;;
  esac
done
emit() { if [ -n "$out" ]; then printf '%s' "$1" >"$out"; else printf '%s' "$1"; fi; }
case "$url" in
  *api.github.com*releases/latest*) emit '{"tag_name":"v9.9.9"}' ;;
  *releases/download*)
    name="$(basename "$url")"
    [ -f "$MOCK_ASSETS/$name" ] || { echo "mock curl: no asset $name" >&2; exit 1; }
    cp "$MOCK_ASSETS/$name" "$out"
    ;;
  *) echo "mock curl: unexpected url $url" >&2; exit 1 ;;
esac
EOF
  cat >"$d/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  -s) printf '%s\n' "${MOCK_UNAME_S:-Linux}" ;;
  -m) printf '%s\n' "${MOCK_UNAME_M:-x86_64}" ;;
  *) exit 1 ;;
esac
EOF
  cat >"$d/shasum" <<'EOF'
#!/usr/bin/env bash
# mock shasum: drop the "-a 256" pair, then delegate to sha256sum (same format)
args=("$@")
out=()
i=0
while [ $i -lt ${#args[@]} ]; do
  if [ "${args[$i]}" = "-a" ] && [ $((i + 1)) -lt ${#args[@]} ] && [ "${args[$((i + 1))]}" = "256" ]; then
    i=$((i + 2)); continue
  fi
  out+=("${args[$i]}")
  i=$((i + 1))
done
exec sha256sum "${out[@]}"
EOF
  cat >"$d/gum" <<'EOF'
#!/usr/bin/env bash
# mock gum: choose prints MOCK_GUM_SELECT, input answers by --header
# (version/dir), confirm exits per MOCK_GUM_CONFIRM.
sub="${1:-}"; shift || true
header=""
while [ $# -gt 0 ]; do
  case "$1" in
    --header) header="$2"; shift 2 ;;
    *) shift ;;
  esac
done
case "$sub" in
  input)
    case "$header" in
      *irectory*) printf '%s\n' "${MOCK_GUM_DIR:-}" ;;
      *) printf '%s\n' "${MOCK_GUM_VERSION:-}" ;;
    esac
    ;;
  choose) printf '%s\n' "${MOCK_GUM_SELECT:-}" ;;
  confirm) [ "${MOCK_GUM_CONFIRM:-0}" = 1 ] ;;
  style) printf '%s\n' "gum style" ;;
  *) exit 1 ;;
esac
EOF
  cat >"$d/dialog" <<'EOF'
#!/usr/bin/env bash
# mock dialog: inputbox returns its default, menu returns MOCK_DIALOG_SELECT,
# and yesno follows MOCK_DIALOG_CONFIRM.
args=("$@")
mode=""
for arg in "${args[@]}"; do
  case "$arg" in
    --inputbox|--menu|--yesno|--infobox) mode="$arg" ;;
  esac
done
case "$mode" in
  --inputbox) printf '%s\n' "${args[$((${#args[@]} - 1))]}" ;;
  --menu) printf '%s\n' "${MOCK_DIALOG_SELECT:-}" ;;
  --yesno) [ "${MOCK_DIALOG_CONFIRM:-0}" = 1 ] ;;
  --infobox) ;;
  *) exit 1 ;;
esac
EOF
  chmod +x "$d/curl" "$d/uname" "$d/shasum" "$d/gum" "$d/dialog"
}

make_release() { # <version> <os> <arch> <outdir> <binaries...>
  local version="$1" os="$2" arch="$3" out="$4"; shift 4
  local bin d
  for bin in "$@"; do
    d="$out/${bin}_${version}_${os}_${arch}"
    mkdir -p "$d"
    # shellcheck disable=SC2016 # the generated mock needs the literal ${1:-}
    printf '#!/usr/bin/env bash\nif [ "${1:-}" = init ]; then\n  if [ "${MOCK_INIT_WRITES_CONFIG:-0}" = 1 ]; then : > homonto.toml; fi\n  echo "%s init fake %s"\nelse\n  echo "%s fake %s"\nfi\n' \
      "$bin" "$version" "$bin" "$version" >"$d/$bin"
    chmod +x "$d/$bin"
    (cd "$out" && tar -czf "${bin}_${version}_${os}_${arch}.tar.gz" "${bin}_${version}_${os}_${arch}")
    rm -rf "$d"
  done
  (cd "$out" && sha256sum ./*.tar.gz > SHA256SUMS)
}

# Bound installer runs so a retry-loop regression fails instead of hanging the suite.
run_with_deadline() {
  local pid watchdog status
  "$@" <&0 &
  pid=$!
  (sleep 10; kill "$pid" 2>/dev/null) &
  watchdog=$!
  wait "$pid"
  status=$?
  kill "$watchdog" 2>/dev/null || true
  wait "$watchdog" 2>/dev/null || true
  return "$status"
}

# run_install <scratch> <answers> [env pairs...] -> sets EXIT/OUT_STDERR/OUT_STDOUT
run_install() {
  local scratch="$1" answers="$2"; shift 2
  local mockbin="$scratch/mockbin"
  mkdir -p "$mockbin" "$scratch/assets"
  make_mocks "$mockbin"
  (cd "$scratch" \
    && printf '%s\n' "$answers" \
    | run_with_deadline env PATH="$mockbin:$PATH" MOCK_ASSETS="$scratch/assets" "$@" "$BASH" "$INSTALLER" \
      >stdout 2>stderr)
  EXIT=$?
  OUT_STDERR="$(cat "$scratch/stderr")"
  OUT_STDOUT="$(cat "$scratch/stdout")"
}

expect_exit() { # <name> <expected>
  if [ "$EXIT" -eq "$2" ]; then ok "$1"; else bad "$1: exit $EXIT, want $2"; printf '%s\n' "$OUT_STDERR" >&2; fi
}

expect_stderr() { # <name> <needle>
  if printf '%s\n' "$OUT_STDERR" | grep -qF -- "$2"; then ok "$1"; else bad "$1 (missing: $2)"; printf '%s\n' "$OUT_STDERR" >&2; fi
}

expect_not_stderr() { # <name> <needle>
  if printf '%s\n' "$OUT_STDERR" | grep -qF -- "$2"; then bad "$1 (unexpected: $2)"; printf '%s\n' "$OUT_STDERR" >&2; else ok "$1"; fi
}

expect_next_steps() { # <present|absent> <name> <command>
  local output="$OUT_STDERR"
  OUT_STDERR="${OUT_STDERR##*Next steps}"
  if [ "$1" = present ]; then expect_stderr "$2" "$3"; else expect_not_stderr "$2" "$3"; fi
  OUT_STDERR="$output"
}

# --- tests -----------------------------------------------------------------

t1_latest_onto_linux() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'\nonto\n'"$s/bin"
  expect_exit "t1: latest + onto installs" 0
  expect_stderr "t1: detects linux/amd64" "platform: linux/amd64"
  expect_stderr "t1: has a plain welcome" "| homonto installer"
  expect_stderr "t1: explains both workflows" "both (recommended): onto for gated work, to for lightweight work."
  expect_stderr "t1: downloads homonto asset" "downloading homonto_v9.9.9_linux_amd64.tar.gz"
  expect_stderr "t1: installs homonto" "installed homonto -> $s/bin/homonto"
  expect_stderr "t1: installs onto" "installed onto -> $s/bin/onto"
  expect_stderr "t1: prints PATH advice" "export PATH=\"$s/bin:\$PATH\""
  expect_next_steps absent "t1: does not advertise unconfigured commands" "/onto"
  expect_next_steps absent "t1: does not advertise unselected commands" "/to"
  if [ -x "$s/bin/homonto" ] && [ -x "$s/bin/onto" ]; then ok "t1: binaries executable"; else bad "t1: binaries executable"; fi
  if [ -z "$OUT_STDOUT" ]; then ok "t1: stdout stays empty"; else bad "t1: stdout stays empty (got: $OUT_STDOUT)"; fi
}

t2_darwin_arm64() {
  local s="$1"
  make_release v9.9.9 darwin arm64 "$s/assets" homonto onto
  run_install "$s" $'\nonto\n'"$s/bin" MOCK_UNAME_S=Darwin MOCK_UNAME_M=arm64
  expect_exit "t2: darwin/arm64 installs" 0
  expect_stderr "t2: detects darwin/arm64" "platform: darwin/arm64"
  expect_stderr "t2: uses darwin assets" "downloading homonto_v9.9.9_darwin_arm64.tar.gz"
}

t3_explicit_version() {
  local s="$1"
  make_release v1.2.3 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'v1.2.3\nonto\n'"$s/bin"
  expect_exit "t3: explicit version installs" 0
  expect_stderr "t3: stamps the pinned version" "homonto fake v1.2.3"
  expect_not_stderr "t3: never consults the API" "downloading homonto_v9.9.9"
}

t4_no_workflow_bin() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'\nnone\n'"$s/bin"
  expect_exit "t4: homonto-only installs" 0
  expect_stderr "t4: installs homonto" "installed homonto -> $s/bin/homonto"
  expect_not_stderr "t4: skips onto" "installed onto"
  expect_not_stderr "t4: no workflow commands without configuration" "/onto"
  if [ -x "$s/bin/homonto" ] && [ ! -e "$s/bin/onto" ]; then ok "t4: only homonto present"; else bad "t4: only homonto present"; fi
}

t5_to_workflow_bin() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto to
  run_install "$s" $'\nto\n'"$s/bin"
  expect_exit "t5: to installs" 0
  expect_stderr "t5: installs to" "installed to -> $s/bin/to"
  if [ -x "$s/bin/to" ]; then ok "t5: to executable"; else bad "t5: to executable"; fi
}

t6_checksum_failure() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  # Tamper the homonto entry: same filename, wrong digest.
  (cd "$s/assets" && sha256sum homonto_v9.9.9_linux_amd64.tar.gz | sed '1s/^/00/' > SHA256SUMS)
  run_install "$s" $'\nonto\n'"$s/bin"
  expect_exit "t6: tampered archive fails closed" 1
  expect_stderr "t6: names the mismatch" "checksum mismatch for homonto_v9.9.9_linux_amd64.tar.gz"
  if [ ! -e "$s/bin/homonto" ]; then ok "t6: nothing installed"; else bad "t6: nothing installed"; fi
}

t7_overwrite_refused() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  mkdir -p "$s/bin"
  printf 'keep me\n' >"$s/bin/homonto"
  run_install "$s" $'\nonto\n'"$s/bin"$'\nn'
  expect_exit "t7: refusing overwrite aborts" 1
  expect_stderr "t7: names the conflict" "homonto already exists at $s/bin/homonto"
  if [ "$(cat "$s/bin/homonto")" = "keep me" ]; then ok "t7: existing binary untouched"; else bad "t7: existing binary untouched"; fi
}

t8_unsupported_os() {
  local s="$1"
  run_install "$s" $'\nonto\n'"$s/bin" MOCK_UNAME_S=FreeBSD
  expect_exit "t8: unsupported OS fails" 1
  expect_stderr "t8: names the OS" "unsupported operating system: FreeBSD"
}

t9_invalid_version_recovers() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'banana\nv9.9.9\nonto\n'"$s/bin"
  expect_exit "t9: bad version then good installs" 0
  expect_stderr "t9: rejects the bad version" 'is not a version like v0.18.0'
  expect_stderr "t9: installs after recovery" "installed homonto -> $s/bin/homonto"
}

t10_shasum_fallback() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'\nonto\n'"$s/bin" HOMONTO_SUM="shasum -a 256"
  expect_exit "t10: shasum fallback verifies" 0
  expect_stderr "t10: installs via shasum" "installed homonto -> $s/bin/homonto"
}

t11_already_on_path() {
  local s="$1"
  local mockbin="$s/mockbin"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'\nonto\n'"$mockbin"
  expect_exit "t11: install into on-PATH dir" 0
  expect_stderr "t11: reports PATH already set" "already on PATH"
  expect_not_stderr "t11: prints no export line" "export PATH="
}

t12_unknown_arg() {
  local rc=0
  "$BASH" "$INSTALLER" bogus >/dev/null 2>"$TMP/stderr12" || rc=$?
  if [ "$rc" -ne 0 ] && grep -qF "unknown argument: bogus" "$TMP/stderr12"; then
    ok "t12: unknown arg rejected"
  else
    bad "t12: unknown arg rejected (rc=$rc)"
  fi
}

t13_help() {
  local rc=0
  "$BASH" "$INSTALLER" --help >"$TMP/stdout13" 2>/dev/null || rc=$?
  if [ "$rc" -eq 0 ] && grep -qF "usage: scripts/install.sh" "$TMP/stdout13"; then
    ok "t13: --help on stdout, exit 0"
  else
    bad "t13: --help on stdout, exit 0 (rc=$rc)"
  fi
}

t14_both_binaries() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  run_install "$s" $'\nboth\n'"$s/bin"
  expect_exit "t14: both installs" 0
  expect_stderr "t14: downloads onto" "downloading onto_v9.9.9_linux_amd64.tar.gz"
  expect_stderr "t14: downloads to" "downloading to_v9.9.9_linux_amd64.tar.gz"
  if [ -x "$s/bin/homonto" ] && [ -x "$s/bin/onto" ] && [ -x "$s/bin/to" ]; then
    ok "t14: all three installed"
  else
    bad "t14: all three installed"
  fi
}

t15_init_confirmed() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  run_install "$s" $'\nnone\n'"$s/bin"$'\ny'
  expect_exit "t15: install + init" 0
  expect_stderr "t15: announces the init run" "running homonto init"
  expect_stderr "t15: runs homonto init" "homonto init fake v9.9.9"
}

t16_init_declined() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  run_install "$s" $'\nnone\n'"$s/bin"$'\nn'
  expect_exit "t16: install without init" 0
  expect_not_stderr "t16: no init run" "homonto init fake"
}

t17_gum_ui() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto to
  run_install "$s" "" HOMONTO_UI=gum MOCK_GUM_SELECT=to MOCK_GUM_DIR="$s/bin" MOCK_GUM_CONFIRM=0
  expect_exit "t17: gum-driven install" 0
  expect_stderr "t17: installs to via gum choice" "installed to -> $s/bin/to"
  expect_not_stderr "t17: no plain prompts" "Install version ["
}

t18_gum_init_confirmed() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  run_install "$s" "" HOMONTO_UI=gum MOCK_GUM_SELECT=none MOCK_GUM_DIR="$s/bin" MOCK_GUM_CONFIRM=1
  expect_exit "t18: gum-driven init" 0
  expect_stderr "t18: runs homonto init via gum confirm" "homonto init fake v9.9.9"
}

t19_forced_gum_requires_binary() {
  local s="$1"
  mkdir -p "$s/empty"
  env PATH="$s/empty" HOMONTO_UI=gum "${BASH:-/bin/bash}" "$INSTALLER" >"$s/stdout" 2>"$s/stderr"
  EXIT=$?
  OUT_STDERR="$(cat "$s/stderr")"
  OUT_STDOUT="$(cat "$s/stdout")"
  expect_exit "t19: forced gum without gum refuses cleanly" 1
  expect_stderr "t19: names the recovery" "HOMONTO_UI=gum requires gum on PATH"
}

t20_guided_project_setup() {
  local s="$1" config
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  mkdir -p "$s/repo-a"
  git -C "$s/repo-a" init -q
  mkdir -p "$s/home/.config/opencode"
  printf '{\n  "model": "test-provider/test-model"\n}\n' >"$s/home/.config/opencode/opencode.json"
  run_install "$s" $'\nboth\n'"$s/bin"$'\ny\nboth\ny\nworkflow\nn\n\n'"$s/repo-a"$'\napi\n' \
    HOME="$s/home" XDG_CONFIG_HOME="$s/home/.config" MOCK_INIT_WRITES_CONFIG=1
  config="$s/homonto.toml"
  expect_exit "t20: guided project setup" 0
  expect_stderr "t20: configures a new project" "Configured homonto.toml"
  expect_stderr "t20: h is a skill bundle" "h GitHub skill bundle"
  expect_not_stderr "t20: h is not a lifecycle workflow" "h GitHub workflows"
  expect_stderr "t20: lifecycle choices are named" "Lifecycle workflows to configure"
  expect_stderr "t20: model scope is disclosed" "global OpenCode settings"
  expect_next_steps present "t20: selected h commands are advertised" "/h-*"
  if grep -qF '[workflow]' "$config" \
    && grep -qF 'root = "workflow"' "$config" \
    && grep -qF '[repos]' "$config" \
    && grep -qF "api = \"$s/repo-a\"" "$config" \
    && grep -qF '[frameworks.onto]' "$config" \
    && grep -qF '[frameworks.to]' "$config" \
    && grep -qF '[frameworks.h]' "$config" \
    && grep -qF '[subagents.homonto.opencode]' "$config" \
    && grep -qF '[subagents.h-spike.opencode]' "$config" \
    && grep -qF 'model = "test-provider/test-model"' "$config" \
    && [ "$(grep -cF '[subagents.homonto.opencode]' "$config")" -eq 1 ]; then
    ok "t20: writes selected repos, workflow root, frameworks, h bundle, and model"
  else
    bad "t20: writes selected repos, workflow root, frameworks, h bundle, and model"
    cat "$config" >&2
  fi
  if (cd "$ROOT" && go build -o "$s/homonto-real" .) \
    && (cd "$s" && "$s/homonto-real" plan >/dev/null 2>"$s/plan.stderr"); then
    ok "t20: generated configuration passes homonto plan"
  else
    bad "t20: generated configuration passes homonto plan"
    cat "$s/plan.stderr" >&2
  fi
}

t21_existing_config_is_unchanged() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  printf '# existing configuration\n' >"$s/homonto.toml"
  run_install "$s" $'\nnone\n'"$s/bin"$'\ny'
  expect_exit "t21: existing project setup" 0
  expect_stderr "t21: names the unchanged config" "homonto.toml already exists"
  expect_not_stderr "t21: does not guess existing workflow configuration" "/onto"
  if [ "$(cat "$s/homonto.toml")" = "# existing configuration" ]; then
    ok "t21: preserves the existing config"
  else
    bad "t21: preserves the existing config"
    cat "$s/homonto.toml" >&2
  fi
}

t22_dialog_ui() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto to
  run_install "$s" "" HOME="$s/home" HOMONTO_UI=dialog MOCK_DIALOG_SELECT=to MOCK_DIALOG_CONFIRM=0
  expect_exit "t22: dialog-driven install" 0
  expect_stderr "t22: installs to via dialog choice" "installed to -> $s/home/.local/bin/to"
  expect_not_stderr "t22: no plain prompts" "Workflow binaries"
}

t23_forced_dialog_requires_binary() {
  local s="$1"
  mkdir -p "$s/empty"
  env PATH="$s/empty" HOMONTO_UI=dialog "${BASH:-/bin/bash}" "$INSTALLER" >"$s/stdout" 2>"$s/stderr"
  EXIT=$?
  OUT_STDERR="$(cat "$s/stderr")"
  OUT_STDOUT="$(cat "$s/stdout")"
  expect_exit "t23: forced dialog without dialog refuses cleanly" 1
  expect_stderr "t23: names the recovery" "HOMONTO_UI=dialog requires dialog on PATH"
}

t24_h_with_onto_configures_transitive_models() {
  local s="$1" config
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  run_install "$s" $'\nboth\n'"$s/bin"$'\ny\nonto\ny\n\nn\n\n' MOCK_INIT_WRITES_CONFIG=1
  config="$s/homonto.toml"
  expect_exit "t24: onto + h guided setup" 0
  expect_next_steps present "t24: h's transitive to workflow is available" "/to"
  if grep -qF '[frameworks.onto]' "$config" \
    && grep -qF '[frameworks.h]' "$config" \
    && ! grep -qF '[frameworks.to]' "$config" \
    && grep -qF '[subagents.to-explorer.opencode]' "$config"; then
    ok "t24: onto + h declares h's transitive to models"
  else
    bad "t24: onto + h declares h's transitive to models"
    cat "$config" >&2
  fi
  if (cd "$ROOT" && go build -o "$s/homonto-real" .) \
    && (cd "$s" && "$s/homonto-real" plan >/dev/null 2>"$s/plan.stderr"); then
    ok "t24: onto + h generated configuration passes homonto plan"
  else
    bad "t24: onto + h generated configuration passes homonto plan"
    cat "$s/plan.stderr" >&2
  fi
}

t25_h_with_to_configures_transitive_models() {
  local s="$1" config
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  run_install "$s" $'\nboth\n'"$s/bin"$'\ny\nto\ny\n\nn\n\n' MOCK_INIT_WRITES_CONFIG=1
  config="$s/homonto.toml"
  expect_exit "t25: to + h guided setup" 0
  expect_next_steps present "t25: h's transitive onto workflow is available" "/onto"
  if grep -qF '[frameworks.to]' "$config" \
    && grep -qF '[frameworks.h]' "$config" \
    && ! grep -qF '[frameworks.onto]' "$config" \
    && grep -qF '[subagents.onto-explorer.opencode]' "$config"; then
    ok "t25: to + h declares h's transitive onto models"
  else
    bad "t25: to + h declares h's transitive onto models"
    cat "$config" >&2
  fi
  if (cd "$ROOT" && go build -o "$s/homonto-real" .) \
    && (cd "$s" && "$s/homonto-real" plan >/dev/null 2>"$s/plan.stderr"); then
    ok "t25: to + h generated configuration passes homonto plan"
  else
    bad "t25: to + h generated configuration passes homonto plan"
    cat "$s/plan.stderr" >&2
  fi
}

t26_guided_tmp_directory() {
  local s="$1" config
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  run_install "$s" $'\nboth\n'"$s/bin"$'\ny\nboth\ny\n\ny\n\n\n' MOCK_INIT_WRITES_CONFIG=1
  config="$s/homonto.toml"
  expect_exit "t26: guided setup with tmp" 0
  if grep -qF '[tmp]' "$config" \
    && grep -qF 'dir = ".tmp"' "$config" \
    && [ "$(grep -cF '[tmp]' "$config")" -eq 1 ]; then
    ok "t26: writes the declared tmp directory"
  else
    bad "t26: writes the declared tmp directory"
    cat "$config" >&2
  fi
  if (cd "$ROOT" && go build -o "$s/homonto-real" .) \
    && (cd "$s" && "$s/homonto-real" plan >/dev/null 2>"$s/plan.stderr"); then
    ok "t26: tmp configuration passes homonto plan"
  else
    bad "t26: tmp configuration passes homonto plan"
    cat "$s/plan.stderr" >&2
  fi
}

# The model question must reject stray confirms and #variant suffixes before
# they are copied to every model block.
t27_model_answer_is_validated() {
  local s="$1" config
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto to
  mkdir -p "$s/home/.config/opencode"
  printf '{\n  "model": "test-provider/default-model"\n}\n' >"$s/home/.config/opencode/opencode.json"
  run_install "$s" $'\nboth\n'"$s/bin"$'\ny\nboth\nn\n\ny\ny\nprovider/model#high\nprovider/real-model\n\n' \
    HOME="$s/home" XDG_CONFIG_HOME="$s/home/.config" MOCK_INIT_WRITES_CONFIG=1
  config="$s/homonto.toml"
  expect_exit "t27: guided setup with stray-y model answer" 0
  expect_stderr "t27: rejects the non-model answer" 'is not a model — use provider/model'
  if grep -qF 'model = "provider/real-model"' "$config" \
    && ! grep -qF 'model = "y"' "$config" \
    && ! grep -qF 'model#high' "$config"; then
    ok "t27: the retried model lands everywhere; no \"y\" poisons the config"
  else
    bad "t27: the retried model lands everywhere; no \"y\" poisons the config"
    cat "$config" >&2
  fi
}

t28_checksum_requires_stdin_operand() {
  local s="$1" real_sum bin
  real_sum="$(command -v sha256sum)"
  make_release v9.9.9 darwin arm64 "$s/assets" homonto onto to
  mkdir -p "$s/mockbin"
  # Issue #7: macOS sha256sum can require an explicit stdin manifest operand.
  # Keep real digest verification so this mock cannot accept corrupted assets.
  cat >"$s/mockbin/sha256sum" <<'EOF'
#!/usr/bin/env bash
if [ "$#" -ne 2 ] || [ "$1" != -c ] || [ "$2" != - ]; then
  printf 'usage: sha256sum -c -\n' >&2
  exit 2
fi
exec "$MOCK_REAL_SHA256SUM" "$@"
EOF
  chmod +x "$s/mockbin/sha256sum"
  run_install "$s" $'\nboth\n'"$s/bin" \
    HOMONTO_SUM= MOCK_UNAME_S=Darwin MOCK_UNAME_M=arm64 MOCK_REAL_SHA256SUM="$real_sum"
  expect_exit "t28: explicit-stdin checksum tool installs on Darwin" 0
  expect_not_stderr "t28: valid archives are not reported as mismatches" "checksum mismatch"
  for bin in homonto onto to; do
    if [ -x "$s/bin/$bin" ]; then ok "t28: $bin installed"; else bad "t28: $bin installed"; fi
  done

  printf 'tampered\n' >>"$s/assets/homonto_v9.9.9_darwin_arm64.tar.gz"
  run_install "$s" $'\nboth\n'"$s/rejected" \
    HOMONTO_SUM= MOCK_UNAME_S=Darwin MOCK_UNAME_M=arm64 MOCK_REAL_SHA256SUM="$real_sum"
  expect_exit "t28: explicit-stdin checksum tool rejects tampering" 1
  expect_stderr "t28: tampering names the mismatch" "checksum mismatch for homonto_v9.9.9_darwin_arm64.tar.gz"
  if [ ! -e "$s/rejected/homonto" ]; then ok "t28: corrupted archive not installed"; else bad "t28: corrupted archive not installed"; fi
}

t29_directory_binary_targets() {
  local s="$1" kind d
  for kind in directory symlink; do
    d="$s/$kind"
    make_release v9.9.9 linux amd64 "$d/assets" homonto
    mkdir -p "$d/bin" "$d/existing"
    if [ "$kind" = directory ]; then
      mkdir -p "$d/bin/homonto"
    else
      ln -s "$d/existing" "$d/bin/homonto"
    fi
    printf 'keep me\n' >"$d/bin/homonto/sentinel"
    run_install "$d" $'\nnone\n'"$d/bin"$'\ny\nn'
    expect_exit "t29: rejects $kind binary target" 1
    expect_stderr "t29: explains $kind target" "binary target is a directory"
    expect_not_stderr "t29: no overwrite prompt for $kind" "overwrite?"
    if [ "$(ls -A "$d/bin/homonto/")" = sentinel ] \
      && [ "$(cat "$d/bin/homonto/sentinel")" = 'keep me' ]; then
      ok "t29: $kind content stays untouched, with no nested binary"
    else
      bad "t29: $kind content stays untouched, with no nested binary"
    fi
  done
}

t30_selection_aware_next_steps() {
  local s="$1" bins setup d h_answer cmd wanted
  for bins in both onto to none; do
    for setup in both onto to none; do
      d="$s/$bins-$setup"
      make_release v9.9.9 linux amd64 "$d/assets" homonto onto to
      h_answer=""
      if [ "$bins" = both ] && [ "$setup" != none ]; then h_answer=$'n\n'; fi
      run_install "$d" $'\n'"$bins"$'\n'"$d/bin"$'\ny\n'"$setup"$'\n'"$h_answer"$'docs\nn\n\n\n' \
        MOCK_INIT_WRITES_CONFIG=1
      expect_exit "t30: $bins binaries + $setup configuration" 0
      # Inspect only the final advice, not prompts describing available choices.
      OUT_STDERR="${OUT_STDERR##*Next steps}"
      expect_not_stderr "t30: $bins/$setup omits unselected h" '/h-*'
      for cmd in onto to; do
        wanted=no
        if { [ "$bins" = both ] || [ "$bins" = "$cmd" ]; } \
          && { [ "$setup" = both ] || [ "$setup" = "$cmd" ]; }; then wanted=yes; fi
        if [ "$wanted" = yes ]; then
          expect_stderr "t30: $bins/$setup advertises configured $cmd" "/$cmd"
        else
          expect_not_stderr "t30: $bins/$setup omits unavailable $cmd" "/$cmd"
        fi
      done
    done
  done
}

t31_repository_validation() {
  local s="$1" kind d path message
  for kind in subdirectory self duplicate numeric; do
    d="$s/$kind"
    make_release v9.9.9 linux amd64 "$d/assets" homonto
    mkdir -p "$d/repo/src"
    git -C "$d" init -q
    git -C "$d/repo" init -q
    ln -s "$d" "$d/self-alias"
    ln -s "$d/repo" "$d/repo-alias"
    case "$kind" in
      subdirectory) path="$d/repo/src"; message="repository path must be a Git worktree root" ;;
      self) path="$d/self-alias"; message="config repository is already included" ;;
      duplicate) path="$d/repo"$'\napi\n'"$d/repo-alias"; message="repository path already selected" ;;
      numeric) path="$d/repo"$'\n123'; message="repository name must not be numeric-only" ;;
    esac
    run_install "$d" $'\nnone\n'"$d/bin"$'\ny\nnone\ndocs\nn\n'"$path"$'\n\n' MOCK_INIT_WRITES_CONFIG=1
    expect_exit "t31: $kind input recovers" 0
    expect_stderr "t31: rejects $kind repository" "$message"
    if [ "$kind" = duplicate ]; then
      if [ "$(grep -c ' = ' "$d/homonto.toml")" -eq 2 ]; then
        ok "t31: duplicate path emitted only once"
      else
        bad "t31: duplicate path emitted only once"
      fi
    elif ! grep -qF '[repos]' "$d/homonto.toml"; then
      ok "t31: $kind repository not written"
    else
      bad "t31: $kind repository not written"
    fi
  done
}

t32_workflow_root_validation() {
  local s="$1" root d i=0
  for root in ./ .// docs/.. records/../. self-alias .tmp ./.tmp/ .tmp/records tmp-alias/records; do
    i=$((i + 1)); d="$s/$i"
    make_release v9.9.9 linux amd64 "$d/assets" homonto
    mkdir -p "$d/.tmp"
    ln -s "$d" "$d/self-alias"
    ln -s "$d/.tmp" "$d/tmp-alias"
    # EOF retries use docs; tmp is accepted only for roots that reach its prompt.
    case "$root" in
      *tmp*)
        run_install "$d" $'\nnone\n'"$d/bin"$'\ny\nnone\n'"$root"$'\ny\ndocs\n\n' MOCK_INIT_WRITES_CONFIG=1
        expect_stderr "t32: rejects records/tmp collision $root" "overlaps the workspace tmp directory"
        ;;
      *)
        run_install "$d" $'\nnone\n'"$d/bin"$'\ny\nnone\n'"$root"$'\ndocs\nn\n\n' MOCK_INIT_WRITES_CONFIG=1
        expect_stderr "t32: rejects root alias $root" "must be a relative path below this repository"
        ;;
    esac
    expect_exit "t32: $root input recovers" 0
    if grep -qF 'root = "docs"' "$d/homonto.toml"; then
      ok "t32: valid retry replaces $root"
    else
      bad "t32: valid retry replaces $root"
    fi
  done
}

t33_relative_repository_with_git_file() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  git init -q --separate-git-dir "$s/git-metadata" "$s/service a"
  run_install "$s" $'\nnone\n'"$s/bin"$'\ny\nnone\n./records//nested/\ny\n./service a\napi-2\nservice a/.\n\n' \
    MOCK_INIT_WRITES_CONFIG=1
  expect_exit "t33: relative repo with spaces and .git file accepted" 0
  expect_stderr "t33: rejects lexical duplicate" "repository path already selected"
  if grep -qF 'api-2 = "./service a"' "$s/homonto.toml" \
    && grep -qF 'root = "./records//nested/"' "$s/homonto.toml" \
    && [ ! -e "$s/records" ]; then
    ok "t33: preserves valid paths without creating records"
  else
    bad "t33: preserves valid paths without creating records"
  fi
}

t34_workflow_root_whitespace() {
  local s="$1" root d i=0
  for root in ' ./ ' ' .tmp ' $'\tdocs' $'docs\t'; do
    i=$((i + 1)); d="$s/$i"
    make_release v9.9.9 linux amd64 "$d/assets" homonto
    run_install "$d" $'\nnone\n'"$d/bin"$'\ny\nnone\n'"$root"$'\ndocs\ny\n\n' MOCK_INIT_WRITES_CONFIG=1
    expect_exit "t34: whitespace input $i recovers" 0
    expect_stderr "t34: whitespace input $i rejected" "must not have surrounding whitespace"
    if grep -qF 'root = "docs"' "$d/homonto.toml" && grep -qF '[tmp]' "$d/homonto.toml"; then
      ok "t34: whitespace input $i replaced before tmp validation"
    else
      bad "t34: whitespace input $i replaced before tmp validation"
    fi
  done
}

t35_unrecoverable_directories() {
  local s="$1" kind d answers message
  for kind in tmp-self tmp-outside docs-file docs-overlap; do
    d="$s/$kind"
    make_release v9.9.9 linux amd64 "$d/assets" homonto
    answers=$'\nnone\n'"$d/bin"$'\ny\nnone'
    case "$kind" in
      tmp-self | tmp-outside)
        if [ "$kind" = tmp-self ]; then ln -s . "$d/.tmp"; else ln -s .. "$d/.tmp"; fi
        answers+=$'\ndocs\ny'
        message="workspace tmp directory must resolve below this repository"
        ;;
      docs-file)
        printf 'keep me\n' >"$d/docs"
        message="end of input"
        ;;
      docs-overlap)
        mkdir -p "$d/docs"
        ln -s docs "$d/.tmp"
        answers+=$'\ndocs\ny'
        message="end of input"
        ;;
    esac
    run_install "$d" "$answers" MOCK_INIT_WRITES_CONFIG=1
    # Avoid dumping the unbounded prompt output when testing a broken retry loop.
    if [ "$EXIT" -eq 1 ] && [[ "$OUT_STDERR" == *"$message"* ]]; then
      ok "t35: $kind terminates with a clear error"
    else
      bad "t35: $kind terminates with a clear error (exit $EXIT)"
    fi
    if [ ! -s "$d/homonto.toml" ]; then
      ok "t35: $kind does not append invalid configuration"
    else
      bad "t35: $kind does not append invalid configuration"
    fi
  done
}

t36_cdpath_repository_resolution() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto
  mkdir -p "$s/repo" "$s/elsewhere/repo"
  git -C "$s/repo" init -q
  run_install "$s" $'\nnone\n'"$s/bin"$'\ny\nnone\ndocs\nn\nrepo\napi\n\n' \
    CDPATH="$s/elsewhere:." MOCK_INIT_WRITES_CONFIG=1
  expect_exit "t36: inherited CDPATH does not affect setup" 0
  if grep -qF 'api = "repo"' "$s/homonto.toml"; then
    ok "t36: relative repository resolves against the config directory"
  else
    bad "t36: relative repository resolves against the config directory"
  fi
  expect_not_stderr "t36: CDPATH does not produce a false root rejection" "must be a Git worktree root"
}

t37_invalid_detected_model_recovers() {
  local s="$1" answer d expected input
  for answer in replacement default; do
    d="$s/$answer"
    make_release v9.9.9 linux amd64 "$d/assets" homonto onto
    mkdir -p "$d/home/.config/opencode"
    printf '{\n  "model": "provider/model#high"\n}\n' >"$d/home/.config/opencode/opencode.json"
    expected=opencode-go/qwen3.7-plus
    [ "$answer" != replacement ] || expected=provider/replacement
    input=""
    [ "$answer" != replacement ] || input="$expected"
    run_install "$d" $'\nonto\n'"$d/bin"$'\ny\nonto\ndocs\nn\n'"$input"$'\n\n' \
      HOME="$d/home" XDG_CONFIG_HOME="$d/home/.config" MOCK_INIT_WRITES_CONFIG=1
    expect_exit "t37: invalid detected model allows $answer" 0
    if grep -qF "model = \"$expected\"" "$d/homonto.toml" && ! grep -qF '#high' "$d/homonto.toml"; then
      ok "t37: $answer uses a valid model"
    else
      bad "t37: $answer uses a valid model"
    fi
  done
}

t38_eof_validation_retries() {
  local s="$1" kind d answers
  for kind in version selection model valid-defaults; do
    d="$s/$kind"
    make_release v9.9.9 linux amd64 "$d/assets" homonto onto
    case "$kind" in
      version) answers=banana ;;
      selection) answers=$'\nbanana' ;;
      model) answers=$'\nonto\n'"$d/bin"$'\ny\nonto\ndocs\nn\ny' ;;
      valid-defaults) answers=$'\nnone\n'"$d/bin"$'\ny\nnone' ;;
    esac
    run_install "$d" "$answers" MOCK_INIT_WRITES_CONFIG=1
    if [ "$kind" = valid-defaults ]; then
      expect_exit "t38: valid setup defaults still work at EOF" 0
      expect_stderr "t38: EOF defaults produce configuration" "Configured homonto.toml"
    else
      expect_exit "t38: $kind retry stops at EOF" 1
      expect_stderr "t38: $kind retry explains EOF" "end of input while retrying"
    fi
  done
}

# --- run -------------------------------------------------------------------

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

t1_latest_onto_linux "$TMP/t1"
t2_darwin_arm64 "$TMP/t2"
t3_explicit_version "$TMP/t3"
t4_no_workflow_bin "$TMP/t4"
t5_to_workflow_bin "$TMP/t5"
t6_checksum_failure "$TMP/t6"
t7_overwrite_refused "$TMP/t7"
t8_unsupported_os "$TMP/t8"
t9_invalid_version_recovers "$TMP/t9"
t10_shasum_fallback "$TMP/t10"
t11_already_on_path "$TMP/t11"
t12_unknown_arg
t13_help
t14_both_binaries "$TMP/t14"
t15_init_confirmed "$TMP/t15"
t16_init_declined "$TMP/t16"
t17_gum_ui "$TMP/t17"
t18_gum_init_confirmed "$TMP/t18"
t19_forced_gum_requires_binary "$TMP/t19"
t20_guided_project_setup "$TMP/t20"
t21_existing_config_is_unchanged "$TMP/t21"
t22_dialog_ui "$TMP/t22"
t23_forced_dialog_requires_binary "$TMP/t23"
t24_h_with_onto_configures_transitive_models "$TMP/t24"
t25_h_with_to_configures_transitive_models "$TMP/t25"
t26_guided_tmp_directory "$TMP/t26"
t27_model_answer_is_validated "$TMP/t27"
t28_checksum_requires_stdin_operand "$TMP/t28"
t29_directory_binary_targets "$TMP/t29"
t30_selection_aware_next_steps "$TMP/t30"
t31_repository_validation "$TMP/t31"
t32_workflow_root_validation "$TMP/t32"
t33_relative_repository_with_git_file "$TMP/t33"
t34_workflow_root_whitespace "$TMP/t34"
t35_unrecoverable_directories "$TMP/t35"
t36_cdpath_repository_resolution "$TMP/t36"
t37_invalid_detected_model_recovers "$TMP/t37"
t38_eof_validation_retries "$TMP/t38"

printf '\n'
for line in "${SUMMARY[@]}"; do printf '%s\n' "$line"; done
printf 'install-test: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
