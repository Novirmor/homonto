#!/usr/bin/env bash
# Mocked-network tests for scripts/install.sh.
#
# Every test runs the installer against a fake $PATH whose curl serves canned
# release archives from a scratch directory and whose uname pins the platform,
# so nothing touches the network or the real filesystem outside the scratch
# dir. Answers are piped on stdin; the installer contract — all output to
# stderr, PATH advice printed not applied, checksum verification enforced — is
# asserted per test. Runs offline; wired into scripts/gate.sh.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
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
  chmod +x "$d/curl" "$d/uname" "$d/shasum" "$d/gum"
}

make_release() { # <version> <os> <arch> <outdir> <binaries...>
  local version="$1" os="$2" arch="$3" out="$4"; shift 4
  local bin d
  for bin in "$@"; do
    d="$out/${bin}_${version}_${os}_${arch}"
    mkdir -p "$d"
    # shellcheck disable=SC2016 # the generated mock needs the literal ${1:-}
    printf '#!/usr/bin/env bash\nif [ "${1:-}" = init ]; then echo "%s init fake %s"; else echo "%s fake %s"; fi\n' \
      "$bin" "$version" "$bin" "$version" >"$d/$bin"
    chmod +x "$d/$bin"
    (cd "$out" && tar -czf "${bin}_${version}_${os}_${arch}.tar.gz" "${bin}_${version}_${os}_${arch}")
    rm -rf "$d"
  done
  (cd "$out" && sha256sum ./*.tar.gz > SHA256SUMS)
}

# run_install <scratch> <answers> [env pairs...] -> sets EXIT/OUT_STDERR/OUT_STDOUT
run_install() {
  local scratch="$1" answers="$2"; shift 2
  local mockbin="$scratch/mockbin"
  mkdir -p "$mockbin" "$scratch/assets"
  make_mocks "$mockbin"
  (cd "$scratch" \
    && printf '%s\n' "$answers" \
    | env PATH="$mockbin:$PATH" MOCK_ASSETS="$scratch/assets" "$@" bash "$INSTALLER" \
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

# --- tests -----------------------------------------------------------------

t1_latest_onto_linux() {
  local s="$1"
  make_release v9.9.9 linux amd64 "$s/assets" homonto onto
  run_install "$s" $'\nonto\n'"$s/bin"
  expect_exit "t1: latest + onto installs" 0
  expect_stderr "t1: detects linux/amd64" "platform: linux/amd64"
  expect_stderr "t1: has a plain welcome" "== homonto installer =="
  expect_stderr "t1: explains both workflows" "both (recommended): onto for gated work, to for lightweight work."
  expect_stderr "t1: downloads homonto asset" "downloading homonto_v9.9.9_linux_amd64.tar.gz"
  expect_stderr "t1: installs homonto" "installed homonto -> $s/bin/homonto"
  expect_stderr "t1: installs onto" "installed onto -> $s/bin/onto"
  expect_stderr "t1: prints PATH advice" "export PATH=\"$s/bin:\$PATH\""
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
  bash "$INSTALLER" bogus >/dev/null 2>"$TMP/stderr12" || rc=$?
  if [ "$rc" -ne 0 ] && grep -qF "unknown argument: bogus" "$TMP/stderr12"; then
    ok "t12: unknown arg rejected"
  else
    bad "t12: unknown arg rejected (rc=$rc)"
  fi
}

t13_help() {
  local rc=0
  bash "$INSTALLER" --help >"$TMP/stdout13" 2>/dev/null || rc=$?
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

printf '\n'
for line in "${SUMMARY[@]}"; do printf '%s\n' "$line"; done
printf 'install-test: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
