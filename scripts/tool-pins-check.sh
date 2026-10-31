#!/usr/bin/env bash
# Every copy of a tool pin must agree with its source, so `mise install`, the
# Taskfile, CI, and the gate never build or lint with different versions.
# Sources: go.mod's toolchain directive (Go) and scripts/gate.sh
# (golangci-lint). Copies: mise.toml and the setup-go steps in the workflows.
set -euo pipefail
cd "$(dirname "$0")/.."

go_pin="$(sed -n 's/^toolchain go//p' go.mod)"
lint_pin="$(sed -n 's/^GOLANGCI_LINT_VERSION=//p' scripts/gate.sh)"
[ -n "$go_pin" ] || { echo "go.mod has no toolchain pin"; exit 1; }
[ -n "$lint_pin" ] || { echo "scripts/gate.sh has no GOLANGCI_LINT_VERSION"; exit 1; }

fails=0
expect() { # file, what, found, want
  if [ "$3" != "$4" ]; then
    echo "$1: $2 is \"$3\", want \"$4\""
    fails=$((fails + 1))
  fi
}

mise_pin() { sed -n "s/^$1 = \"\(.*\)\"$/\1/p" mise.toml; }
expect mise.toml go "$(mise_pin go)" "$go_pin"
expect mise.toml golangci-lint "$(mise_pin golangci-lint)" "$lint_pin"

for wf in .github/workflows/*.yml; do
  while IFS= read -r v; do
    expect "$wf" go-version "$v" "$go_pin"
  done < <(sed -n 's/^ *go-version: "\(.*\)"$/\1/p' "$wf")
done

if [ "$fails" -ne 0 ]; then
  echo "tool-pins-check: $fails pin(s) drifted from go.mod / scripts/gate.sh"
  exit 1
fi
echo "tool-pins-check: mise.toml and workflows match go $go_pin, golangci-lint $lint_pin"
