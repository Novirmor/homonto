#!/usr/bin/env bash
# Real dual-binary E2E matrix runner.
#
# Builds one image containing homonto + onto (from source) and the ACTUAL
# OpenCode CLI, then runs a matrix of suites against it:
#
#   suite \ tool   opencode   claude
#   projection     ✓          ✓        (homonto apply → real tool reads config)
#   live           ✓                   (real account prompt → PONG)
#   recovery       ✓          ✓        (start/resume/compaction context, runtime tool)
#   policy                    ✓        (worker policy denials and handback from real hooks)
#   process        ✓          ✓        (a builtin to process run by the model via /to)
#   interrupt      ✓          ✓        (the process killed at a state boundary, then resumed)
#   plumbing       shared, offline, no account (the machinery the cells above assert with)
#   onto           shared, tool-independent (full open→…→close workflow)
#   reference      shared (remote reference set, both harnesses, native lifecycle)
#
# The `live` cells reuse the invoking user's own accounts by mounting their
# credentials READ-ONLY at run time (never baked into the image). OpenCode uses a
# free model and Claude Code uses haiku with bounded turns; every harness call
# and every cell has a hard timeout. Finally it captures a structured dump of
# the container's internal state (the `analyze` suite).
#
# Evidence: every cell's outcome and every scenario a suite reports
# (E2E-EVIDENCE lines, suites/lib.sh) land in test/e2e/.out/evidence.tsv,
# keyed to the commit under test; the summary prints the release evidence
# key (e2e/<cell>, scripts/releaseevidence) for each cell that has one.
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
OPENCODE_MODEL="${E2E_OPENCODE_MODEL:-opencode/deepseek-v4.1-flash-free}"
CLAUDE_AUTH="${CLAUDE_AUTH:-$HOME/.claude/.credentials.json}"
CELL_TIMEOUT="${E2E_CELL_TIMEOUT:-2400}"   # seconds per cell, container included

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
COMMIT="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)"
DIRTY="$(git -C "$ROOT" status --porcelain --untracked-files=no 2>/dev/null | head -1)"
TREE=clean; [ -n "$DIRTY" ] && TREE=dirty
EVIDENCE="$OUT/evidence.tsv"
{
  printf '# e2e matrix evidence: commit=%s tree=%s started=%s\n' "$COMMIT" "$TREE" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'kind\tid\tstatus\tcell\trelease_key\tlog\tnote\n'
} > "$EVIDENCE"

# release_key <cell>: the release evidence key the cell produces, or "-".
# The keys are declared once, in internal/starter (HarnessEvidence).
release_key() {
  if grep -q "\"e2e/$1\"" "$ROOT/internal/starter/starter.go"; then printf 'e2e/%s' "$1"; else printf -- '-'; fi
}

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
  local log="$OUT/${suite}${tool:+-$tool}.log" name="homonto-e2e-${suite}${tool:+-$tool}-$$" status
  printf '\n\033[1m▶ %s\033[0m\n' "$cell"
  if timeout --kill-after=30 "$CELL_TIMEOUT" docker run --rm --name "$name" \
      -e E2E_SUITE="$suite" -e E2E_TOOL="$tool" -e E2E_OPENCODE_MODEL="$OPENCODE_MODEL" \
      "$@" "$IMAGE" >"$log" 2>&1; then
    tail -3 "$log" | sed 's/^/   /'
    status=PASS
  else
    docker rm -f "$name" >/dev/null 2>&1 || true
    echo "   --- last 15 lines ($log) ---"
    tail -15 "$log" | sed 's/^/   /'
    status=FAIL
  fi
  RESULTS+=("$cell	$status")
  local key; key="$(release_key "$cell")"
  # Scenario lines the suite printed, then the cell's own line.
  grep -a '^E2E-EVIDENCE	' "$log" | while IFS=$'\t' read -r _ id st note; do
    printf 'scenario\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$st" "$cell" "$key" "${log#"$ROOT"/}" "$note"
  done >> "$EVIDENCE"
  printf 'cell\t%s\t%s\t%s\t%s\t%s\t-\n' "$cell" "$(tr '[:upper:]' '[:lower:]' <<<"$status")" "$cell" "$key" "${log#"$ROOT"/}" >> "$EVIDENCE"
}

skip_cell() { # <cell> <why>
  RESULTS+=("$1	SKIP"); printf '\n▶ %s  (SKIPPED: %s)\n' "$1" "$2"
  printf 'cell\t%s\tskip\t%s\t%s\t-\t%s\n' "$1" "$1" "$(release_key "$1")" "$2" >> "$EVIDENCE"
}

# credentialed_cell <suite> <tool>: run a cell that needs the tool's mounted
# account, or record why it was skipped. A skip is never a pass.
credentialed_cell() {
  local suite="$1" tool="$2" cell="$1/$2"
  selected "$cell" || return 0
  if [ "$SKIP_LIVE" -eq 1 ]; then skip_cell "$cell" "--skip-live"; return 0; fi
  case "$tool" in
    opencode)
      if [ -f "$OPENCODE_AUTH" ]; then
        run_cell "$suite" opencode -v "$OPENCODE_AUTH:/root/.local/share/opencode/auth.json:ro"
      else
        skip_cell "$cell" "no opencode auth at $OPENCODE_AUTH"
      fi ;;
    claude)
      if [ -f "$CLAUDE_AUTH" ]; then
        run_cell "$suite" claude -v "$CLAUDE_AUTH:/root/.claude/.credentials.json:ro"
      else
        skip_cell "$cell" "no claude credentials at $CLAUDE_AUTH"
      fi ;;
  esac
}

if [ "$DO_BUILD" -eq 1 ]; then
  echo "▶ building $IMAGE (homonto + onto + real opencode) ..."
  docker build -f "$ROOT/test/e2e/Dockerfile" -t "$IMAGE" "$ROOT" \
    || { echo "image build failed" >&2; exit 1; }
fi

# --- matrix -----------------------------------------------------------------
run_cell projection opencode
run_cell projection claude
run_cell onto ""
run_cell reference ""
# The credential-free machinery of the process/interrupt/recovery cells
# (shims, boundary hold, hook filters, OpenCode command expansion), offline.
run_cell plumbing "" --network none

credentialed_cell live opencode

# Real-harness recovery (M4.11): startup, resume and compaction context on
# both harnesses, and a runtime tool call from inside a session.
credentialed_cell recovery opencode
credentialed_cell recovery claude

# Real-harness worker policy (M4.5, ADR 0105): the coordinator dispatches the
# onto implementer, and homonto's hooks deny its record write and push and
# hand its refused permission prompt back.
credentialed_cell policy claude

# Builtins inside real sessions (M4.11, M4.5): the model runs the projected
# /to command through to's plan → do, and on Claude Code under homonto's
# hooks; then the same process is killed at a state boundary and resumed.
credentialed_cell process opencode
credentialed_cell process claude
credentialed_cell interrupt opencode
credentialed_cell interrupt claude

# --- container internals analysis ------------------------------------------
if selected analyze || [ ${#SELECT[@]} -eq 0 ]; then
  printf '\n\033[1m▶ analyze (container internals)\033[0m\n'
  docker run --rm -e E2E_SUITE=analyze "$IMAGE" >"$OUT/analysis.txt" 2>&1 || true
  cat "$OUT/analysis.txt"
fi

# --- summary ----------------------------------------------------------------
printf '\n\033[1m================ E2E MATRIX RESULTS ================\033[0m\n'
fails=0; skips=0
for r in "${RESULTS[@]}"; do
  cell="${r%%	*}"; status="${r##*	}"
  case "$status" in
    PASS) mark="✓ PASS" ;;
    FAIL) mark="✗ FAIL"; fails=$((fails+1)) ;;
    *)    mark="- SKIP"; skips=$((skips+1)) ;;
  esac
  printf '  %-6s  %-22s %s\n' "$mark" "$cell" "$(release_key "$cell")"
done
printf '  logs: %s\n  evidence: %s (commit %s%s)\n' "$OUT" "$EVIDENCE" "$COMMIT" "${DIRTY:+, dirty tree: not release evidence}"
if [ "$fails" -gt 0 ]; then
  printf '\n%d cell(s) FAILED\n' "$fails"; exit 1
fi
if [ "$skips" -gt 0 ]; then
  printf '\nALL RUN CELLS PASSED (%d skipped: not evidence)\n' "$skips"
else
  printf '\nALL CELLS PASSED\n'
fi
