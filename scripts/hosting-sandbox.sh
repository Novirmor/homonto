#!/usr/bin/env bash
# Hosting workflow verification against real sandbox repositories on GitHub,
# GitLab.com and Bitbucket Cloud (roadmap M1.8). Local release check, not CI:
# it needs sandbox credentials and posts to (then cleans up) real repositories.
#
# Usage:
#   scripts/hosting-sandbox.sh [--no-build] [--write] [github|gitlab|bitbucket ...]
#
# Reads fixtures from test/e2e/hosting/sandbox.env (see sandbox.env.example).
# Credentials come from the invoking environment and are passed by name only:
# GH_TOKEN, GITLAB_TOKEN, HOMONTO_BITBUCKET_TOKEN (+ HOMONTO_BITBUCKET_EMAIL).
# OpenCode's auth (~/.local/share/opencode/auth.json) is mounted read-only for
# the process layer; without it only the runtime layer runs.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${HOSTING_SANDBOX_ENV:-$ROOT/test/e2e/hosting/sandbox.env}"
IMAGE="${HOMONTO_HOSTING_IMAGE:-homonto-hosting-e2e}"
BASE="${HOMONTO_E2E_IMAGE:-homonto-e2e}"
OUT="$ROOT/test/e2e/.out"
OPENCODE_AUTH="${OPENCODE_AUTH:-$HOME/.local/share/opencode/auth.json}"
DIFFS="docs/guides/hosting-differences.md"

DO_BUILD=1 WRITE=0 HOSTS=()
for a in "$@"; do
  case "$a" in
    --no-build) DO_BUILD=0 ;;
    --write) WRITE=1 ;;
    github | gitlab | bitbucket) HOSTS+=("$a") ;;
    *) echo "unknown argument: $a" >&2; exit 2 ;;
  esac
done
[ ${#HOSTS[@]} -gt 0 ] || HOSTS=(github gitlab bitbucket)
[ -f "$ENV_FILE" ] || { echo "hosting-sandbox: $ENV_FILE not found; copy test/e2e/hosting/sandbox.env.example" >&2; exit 2; }
set -a; # shellcheck disable=SC1090
source "$ENV_FILE"; set +a
mkdir -p "$OUT"

if [ "$DO_BUILD" -eq 1 ]; then
  docker build -f "$ROOT/test/e2e/Dockerfile" -t "$BASE" "$ROOT" || exit 1
  docker build -f "$ROOT/test/e2e/hosting/Dockerfile" --build-arg BASE="$BASE" -t "$IMAGE" "$ROOT" || exit 1
fi

RESULTS=()
for host in "${HOSTS[@]}"; do
  case "$host" in
    github) p=GH; creds=(GH_TOKEN) ;;
    gitlab) p=GL; creds=(GITLAB_TOKEN) ;;
    bitbucket) p=BB; creds=(HOMONTO_BITBUCKET_TOKEN HOMONTO_BITBUCKET_EMAIL) ;;
  esac
  repo_var="HOSTING_${p}_REPO_URL"
  if [ -z "${!repo_var:-}" ] || [ -z "${!creds[0]:-}" ]; then
    RESULTS+=("$host	SKIP (no sandbox or ${creds[0]})"); continue
  fi
  args=(-e E2E_SUITE=hosting -e "E2E_HOST=$host" -e "HOSTING_WRITE=$WRITE")
  for c in "${creds[@]}"; do args+=(-e "$c"); done
  # Map HOSTING_<P>_* to the suite's HOSTING_* names.
  while IFS='=' read -r name _; do
    args+=(-e "HOSTING_${name#HOSTING_${p}_}=${!name}")
  done < <(env | grep "^HOSTING_${p}_" | sort)
  [ -f "$OPENCODE_AUTH" ] && args+=(-v "$OPENCODE_AUTH:/root/.local/share/opencode/auth.json:ro")
  log="$OUT/hosting-$host.log"
  printf '\n\033[1m▶ hosting/%s\033[0m\n' "$host"
  if docker run --rm "${args[@]}" "$IMAGE" >"$log" 2>&1; then
    tail -3 "$log" | sed 's/^/   /'; RESULTS+=("$host	PASS")
  else
    tail -20 "$log" | sed 's/^/   /'; RESULTS+=("$host	FAIL")
  fi
done

printf '\n\033[1m============ HOSTING SANDBOX RESULTS ============\033[0m\n'
fails=0
for r in "${RESULTS[@]}"; do
  printf '  %-34s %s\n' "${r##*	}" "hosting/${r%%	*}"
  case "$r" in *"	FAIL") fails=$((fails + 1)) ;; esac
done
printf '  logs: %s\n  provider differences: %s\n' "$OUT" "$DIFFS"
[ "$fails" -eq 0 ] || { printf '\n%d host(s) FAILED\n' "$fails"; exit 1; }
