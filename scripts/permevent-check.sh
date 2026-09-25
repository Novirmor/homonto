#!/bin/sh
# The V2 observer counts only exact-command, correlated `once` approvals.
set -eu
cd "$(dirname "$0")/.."

FIXTURE=internal/permevent/testdata/opencode-v2-producer.json
[ -f "$FIXTURE" ] || { echo "permevent-check: missing fixture $FIXTURE" >&2; exit 1; }
command -v node >/dev/null || { echo "permevent-check: Node required for V2 plugin runtime test" >&2; exit 1; }
node -e 'if (!process.features.typescript) process.exit(1)' || {
	echo "permevent-check: Node with native TypeScript stripping required" >&2
	exit 1
}

go test ./internal/permevent/ -run 'Test(V2ProducerContract|PermissionV2PluginRuntime)$' -count=1
echo "permevent-check passed: V2 producer pin and runtime observer green"
