#!/usr/bin/env bash
# Allocate after the gate, from remote tags rather than a possibly stale checkout.
set -euo pipefail
remote="${1:?usage: next-release-version.sh <remote>}"
tags="$(git ls-remote --tags --refs "$remote" 'refs/tags/homonto-next-*')"
highest="$(printf '%s\n' "$tags" | sed -nE 's|^[^[:space:]]+[[:space:]]+refs/tags/homonto-next-([1-9][0-9]*)$|\1|p' | sort -n | tail -n 1)"
printf 'homonto-next-%s\n' "$(( ${highest:-0} + 1 ))"
