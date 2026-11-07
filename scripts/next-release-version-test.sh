#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
git init -q --bare "$tmp/remote.git"
git init -q "$tmp/work"
cd "$tmp/work"
git -c user.name=test -c user.email=test@example.com commit -q --allow-empty -m initial
git remote add origin "$tmp/remote.git"
version() { bash "$root/scripts/next-release-version.sh" origin; }
[ "$(version)" = homonto-next-1 ]
git tag homonto-next-2
git tag homonto-next-10
git tag homonto-next-01
git tag homonto-next-999-extra
git tag v99.0.0
git push -q origin --tags
[ "$(version)" = homonto-next-11 ]
if bash "$root/scripts/next-release-version.sh" "$tmp/missing" >/dev/null 2>&1; then
  echo 'missing remote unexpectedly succeeded' >&2
  exit 1
fi
echo 'next release version tests passed'
