#!/usr/bin/env bash
# Real dual-binary E2E matrix runner.
#
# Builds one image containing homonto + onto (from source) and the ACTUAL
# OpenCode CLI, then runs a matrix of suites against it:
#
#   suite \ tool   opencode
#   projection     ✓          (homonto apply → real tool reads config)
#   live           ✓          (real account prompt → PONG)
#   onto           shared, tool-independent (full open→…→close workflow)
#
# The `live` cells reuse the invoking user's own accounts by mounting their
# credentials READ-ONLY at run time (never baked into the image). OpenCode uses a
# free model, so the live checks cost nothing. Finally it captures a structured
# dump of the container's internal state (the `analyze` suite).
#
# Usage:
#   scripts/e2e-matrix.sh [--no-build] [--skip-live] [suite[/tool] ...]
# Examples:
#   scripts/e2e-matrix.sh                 # build + full matrix + analysis
#   scripts/e2e-matrix.sh --no-build onto # rerun just the onto suite
#   scripts/e2e-matrix.sh projection/opencode live/opencode
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${HOMONTO_E2E_IMAGE:-homonto-e2e}"
OUT="$ROOT/test/e2e/.out"
OPENCODE_AUTH="${OPENCODE_AUTH:-$HOME/.local/share/opencode/auth.json}"
OPENCODE_MODEL="${E2E_OPENCODE_MODEL:-opencode/north-mini-code-free}"

DO_BUILD=1
SKIP_LIVE=0
SELECT=()
for a in "$@"; do
  case "$a" in
    --no-build) DO_BUILD=0 ;;
    --skip-live) SKIP_LIVE=1 ;;
    -*) echo "unknown flag: $a" >&2; exit 2 ;;
    *) SELECT+=("$a") ;;
  esac
done

mkdir -p "$OUT"
RESULTS=()  # "suite/tool<TAB>PASS|FAIL|SKIP"

# selected <cell> → 0 if it should run (no selection = all)
selected() {
  [ ${#SELECT[@]} -eq 0 ] && return 0
  for s in "${SELECT[@]}"; do [ "$s" = "$1" ] && return 0; done
  return 1
}

run_cell() { # <suite> <tool> <mount-args...>
  local suite="$1" tool="$2"; shift 2
  local cell="$suite${tool:+/$tool}"
  selected "$cell" || return 0
  local log="$OUT/${suite}${tool:+-$tool}.log"
  printf '\n\033[1m▶ %s\033[0m\n' "$cell"
  if docker run --rm \
      -e E2E_SUITE="$suite" -e E2E_TOOL="$tool" -e E2E_OPENCODE_MODEL="$OPENCODE_MODEL" \
      "$@" "$IMAGE" >"$log" 2>&1; then
    tail -3 "$log" | sed 's/^/   /'
    RESULTS+=("$cell	PASS")
  else
    echo "   --- last 15 lines ($log) ---"
    tail -15 "$log" | sed 's/^/   /'
    RESULTS+=("$cell	FAIL")
  fi
}

skip_cell() { RESULTS+=("$1	SKIP"); printf '\n▶ %s  (SKIPPED)\n' "$1"; }

if [ "$DO_BUILD" -eq 1 ]; then
  echo "▶ building $IMAGE (homonto + onto + real opencode) ..."
  docker build -f "$ROOT/test/e2e/Dockerfile" -t "$IMAGE" "$ROOT" \
    || { echo "image build failed" >&2; exit 1; }
fi

# --- matrix -----------------------------------------------------------------
run_cell projection opencode
run_cell onto ""

if [ "$SKIP_LIVE" -eq 1 ]; then
  skip_cell live/opencode
else
  if [ -f "$OPENCODE_AUTH" ]; then
    run_cell live opencode -v "$OPENCODE_AUTH:/root/.local/share/opencode/auth.json:ro"
  else
    echo "   (no opencode auth at $OPENCODE_AUTH — skipping live/opencode)"; skip_cell live/opencode
  fi
fi

# --- container internals analysis ------------------------------------------
if selected analyze || [ ${#SELECT[@]} -eq 0 ]; then
  printf '\n\033[1m▶ analyze (container internals)\033[0m\n'
  docker run --rm -e E2E_SUITE=analyze "$IMAGE" >"$OUT/analysis.txt" 2>&1 || true
  cat "$OUT/analysis.txt"
fi

# --- summary ----------------------------------------------------------------
printf '\n\033[1m================ E2E MATRIX RESULTS ================\033[0m\n'
fails=0
for r in "${RESULTS[@]}"; do
  cell="${r%%	*}"; status="${r##*	}"
  case "$status" in
    PASS) mark="✓ PASS" ;;
    FAIL) mark="✗ FAIL"; fails=$((fails+1)) ;;
    *)    mark="- SKIP" ;;
  esac
  printf '  %-6s  %s\n' "$mark" "$cell"
done
printf '  logs: %s\n' "$OUT"
if [ "$fails" -gt 0 ]; then
  printf '\n%d cell(s) FAILED\n' "$fails"; exit 1
fi
printf '\nALL CELLS PASSED\n'
