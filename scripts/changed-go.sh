#!/usr/bin/env bash
# Lists the existing Go files changed on this branch relative to BASE (default
# main), plus uncommitted and untracked work. BASE falls back to origin/BASE when
# there is no local branch; an unresolvable BASE fails rather than reporting no
# changes.
#
# Usage: scripts/changed-go.sh [BASE]
set -euo pipefail
cd "$(dirname "$0")/.."

base="${1:-main}"
if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  if git rev-parse --verify --quiet "origin/$base^{commit}" >/dev/null; then
    base="origin/$base"
  else
    echo "changed-go: cannot resolve $base or origin/$base; fetch it or pass BASE=<ref>" >&2
    exit 1
  fi
fi

{
  git diff --name-only "$base...HEAD"
  git diff --name-only HEAD
  git ls-files --others --exclude-standard
} | { grep '\.go$' || true; } | sort -u | while read -r f; do
  if [ -e "$f" ]; then echo "$f"; fi
done
