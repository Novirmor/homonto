#!/usr/bin/env bash
# The single, shared gate. CI, local rehearsal, and the release workflow
# all run THIS script, so no path can publish on a weaker check set than a
# pull request had to pass.
#
# It needs a Go toolchain and nothing else. The product it gates is one
# static binary with no daemon and no services, so a gate that needed
# Docker would be testing the harness rather than the program.
#
# What it does NOT cover, deliberately: the live host matrix — real Claude
# Code and OpenCode sessions on Linux and macOS. Those cannot run
# unattended and cannot be faked convincingly, so they are evidence a human
# produces and scripts/release-guard.sh refuses to release without.
#
# Usage: scripts/gate.sh [--quick]
#   --quick  skip fuzzing and govulncheck (for a fast local loop, never CI)
set -euo pipefail
cd "$(dirname "$0")/.."

QUICK=""
[ "${1:-}" = "--quick" ] && QUICK=1

step() {
  printf '\n============================================================\n=== gate: %s\n============================================================\n' "$1"
}

step "gofmt -l"
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "these files are not gofmt-formatted:"; echo "$unformatted"; exit 1
fi

step "go mod tidy -diff"
go mod tidy -diff

step "go vet ./..."
go vet ./...

step "go build ./..."
go build ./...

step "shell syntax"
for script in scripts/*.sh; do
  bash -n "$script"
done
echo "every script parses"

step "go test ./..."
go test ./... -count=1

step "go test -race ./..."
go test -race ./... -count=1

step "version stamp smoke"
stamped="$(mktemp -d)/homonto"
go build -ldflags "-X github.com/noviopenworks/homonto/internal/cli.Version=gate-smoke" -o "$stamped" .
"$stamped" version 2>&1 | grep -q "gate-smoke" || { echo "version is not stamped"; exit 1; }

step "read-only smoke (a plain directory must stay plain)"
# The probe a host runs before every session must not create a workspace.
# It is the one command that runs constantly, in directories that have
# nothing to do with Homonto.
plain="$(mktemp -d)"
before="$(ls -A "$plain" | wc -l)"
"$stamped" host probe --workspace "$plain" >/dev/null 2>&1 || true
"$stamped" version >/dev/null
after="$(ls -A "$plain" | wc -l)"
if [ "$before" != "$after" ]; then
  echo "a read-only command wrote into a plain directory:"; ls -A "$plain"; exit 1
fi
echo "a plain directory is still plain"

step "release packaging (one native target, signed and verified)"
go test ./test/release/ -count=1

step "documentation describes this product"
go test ./test/docs/ -count=1

step "whole-program security boundaries"
go test ./test/security/ -count=1

if [ -n "$QUICK" ]; then
  printf '\n=== gate: --quick skipped fuzzing and govulncheck. This is NOT a release gate.\n'
  exit 0
fi

step "fuzz every target for 30s"
./scripts/fuzz.sh 30

step "govulncheck ./..."
# Pin the toolchain for the tool build too: with GOTOOLCHAIN=auto, x/vuln's
# own go.mod can select a toolchain older than this module's pin, and a
# govulncheck built that way cannot parse the pinned toolchain's stdlib.
GOTOOLCHAIN=go1.26.6 go run golang.org/x/vuln/cmd/govulncheck@latest ./...

printf '\n============================================================\nALL GATE CHECKS PASSED\n============================================================\n'
