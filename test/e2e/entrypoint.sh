#!/usr/bin/env bash
# E2E image entrypoint. Selects a suite by $E2E_SUITE (or the first arg) and runs
# it with a stable PATH so homonto/onto/opencode all resolve. $E2E_TOOL
# parametrizes the tool-specific suites (projection, live).
set -uo pipefail
export HOME=/root
export PATH="/root/.opencode/bin:/usr/local/bin:/usr/bin:/bin"

SUITE="${E2E_SUITE:-${1:-}}"
export E2E_TOOL="${E2E_TOOL:-}"

if [ -z "$SUITE" ]; then
  echo "e2e: E2E_SUITE (or first arg) required — one of: projection live onto analyze" >&2
  exit 2
fi
S="/opt/e2e-suites/${SUITE}.sh"
if [ ! -f "$S" ]; then
  echo "e2e: unknown suite '$SUITE'" >&2
  exit 2
fi

printf '### e2e suite=%s tool=%s ###\n' "$SUITE" "${E2E_TOOL:-n/a}"
exec bash "$S"
