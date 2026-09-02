#!/bin/sh
# F1 gate: the OpenCode permission-telemetry contract must stay pinned and
# tested. Fails when the fixture, the parser tests, or the pinned revision
# constant is absent — the release contract blocks permission learning on an
# authoritative event, and this check keeps that dependency honest.
set -eu
cd "$(dirname "$0")/.."

FIXTURE=internal/permevent/testdata/opencode_events.jsonl
[ -f "$FIXTURE" ] || { echo "permevent-check: missing fixture $FIXTURE" >&2; exit 1; }

grep -q 'PinnedOpencodeRevision = "[0-9a-f]\{40\}"' internal/permevent/permevent.go || {
	echo "permevent-check: missing 40-char PinnedOpencodeRevision in internal/permevent/permevent.go" >&2
	exit 1
}

grep -q 'permission.asked' "$FIXTURE" && grep -q 'permission.replied' "$FIXTURE" || {
	echo "permevent-check: fixture does not carry both event types" >&2
	exit 1
}

go test ./internal/permevent/ >/dev/null
echo "permevent-check passed: contract fixture present, revision pinned, parser green"
