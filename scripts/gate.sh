#!/usr/bin/env bash
# The single, shared release/CI gate. Runs every check that must pass before a
# tag can publish, in one command, so local rehearsal, CI, and release
# publication all run the SAME gate — no path is weaker than another (closing the
# release-workflow-weaker-than-CI defect). Requires a Go toolchain and a Docker
# daemon (the triple-binary E2E suites build an image).
#
# Usage: scripts/gate.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# Smoke binaries and configs go to a private temp dir, removed on exit, so
# concurrent gates never share /tmp paths.
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Tools built here use go.mod's pinned toolchain, the one build and test use.
toolchain="$(sed -n 's/^toolchain //p' go.mod)"
[ -n "$toolchain" ] || { echo "go.mod has no toolchain pin"; exit 1; }

step() { printf '\n============================================================\n=== gate: %s\n============================================================\n' "$1"; }

step "gofmt -l"
unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
  echo "these files are not gofmt-formatted:"; echo "$unformatted"; exit 1
fi

step "go mod tidy -diff"
go mod tidy -diff

step "go vet ./..."
go vet ./...

step "golangci-lint (.golangci.yml)"
# Pinned like the toolchain so CI, release, and local runs lint identically.
# A matching golangci-lint on PATH (`task tools`) skips the source build.
# This is the only pin; Taskfile.yml reads it from here.
GOLANGCI_LINT_VERSION=2.12.2
if command -v golangci-lint >/dev/null && golangci-lint version 2>/dev/null | grep -q "version $GOLANGCI_LINT_VERSION "; then
  golangci-lint run ./...
else
  GOTOOLCHAIN="$toolchain" go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$GOLANGCI_LINT_VERSION" run ./...
fi

step "go build ./..."
go build ./...

step "go test ./..."
go test ./... -count=1

step "go test -race ./..."
go test -race ./... -count=1 -timeout=20m

step "version stamp smoke (homonto + onto + to)"
go build -ldflags "-X github.com/noviopenworks/homonto/internal/cli.Version=gate-smoke" -o "$work/homonto" .
"$work/homonto" version 2>&1 | grep -q "gate-smoke" || { echo "homonto version not stamped"; exit 1; }
go build -ldflags "-X github.com/noviopenworks/homonto/internal/ontocli.Version=gate-smoke" -o "$work/onto" ./cmd/onto
"$work/onto" version 2>&1 | grep -q "gate-smoke" || { echo "onto version not stamped"; exit 1; }
go build -ldflags "-X github.com/noviopenworks/homonto/internal/tocli.Version=gate-smoke" -o "$work/to" ./cmd/to
"$work/to" version 2>&1 | grep -q "gate-smoke" || { echo "to version not stamped"; exit 1; }

step "cli smoke (plan on a current-format config)"
printf '[settings.opencode]\nshare = "manual"\n' > "$work/homonto.toml"
"$work/homonto" --config "$work/homonto.toml" plan >/dev/null

step "agent docs reference only things that exist"
./scripts/agents-doc-check.sh

step "tool pins agree (mise.toml, workflows)"
./scripts/tool-pins-check.sh

step "workflow skills shell out (no direct state writes)"
./scripts/onto-skills-shell-out-check.sh

step "opencode permission-telemetry contract stays pinned and tested"
./scripts/permevent-check.sh

step "installer mocked-network tests"
./scripts/install-test.sh

step "govulncheck ./..."
# Pin the toolchain for the tool build too: with GOTOOLCHAIN=auto, x/vuln's own
# go.mod can select a toolchain older than this module's pin, and a govulncheck
# built that way cannot parse the pinned toolchain's stdlib sources.
GOTOOLCHAIN="$toolchain" go run golang.org/x/vuln/cmd/govulncheck@latest ./...

step "triple-binary Docker E2E suites (including release-packaging smoke)"
./scripts/docker-test.sh

printf '\n============================================================\nALL GATE CHECKS PASSED\n============================================================\n'
