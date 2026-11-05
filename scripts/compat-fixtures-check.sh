#!/usr/bin/env bash
# Frozen compatibility fixtures (ADR 0109) may gain files, never change or
# lose one. Compares testdata/compat/ against BASE (default: the most recent
# release tag reachable from HEAD).
set -euo pipefail
cd "$(dirname "$0")/.."

base="${1:-${COMPAT_BASE:-}}"
if [ -z "$base" ]; then
	if ! base="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null)"; then
		echo "compat-fixtures-check: no release tag reachable from HEAD; fetch tags or pass a base ref" >&2
		exit 1
	fi
fi
git rev-parse --verify --quiet "$base^{commit}" >/dev/null || {
	echo "compat-fixtures-check: base $base does not resolve" >&2
	exit 1
}

changed="$(git diff --name-only --diff-filter=MDRT "$base" -- testdata/compat/)"
if [ -n "$changed" ]; then
	echo "compat-fixtures-check: frozen fixtures changed since $base (ADR 0109):" >&2
	echo "$changed" >&2
	echo "A failing internal/compat test means the change breaks 1.x; fix the change, not the fixture." >&2
	exit 1
fi
echo "compat-fixtures-check: testdata/compat unchanged since $base (additions only)"
