#!/usr/bin/env bash
# Run every fuzz target for a fixed time.
#
# Fuzzing here is not a search for exotic bugs; it is the check that the
# parsers and the two state machines cannot be driven into a panic by input
# that arrives from outside the binary — a document an agent wrote, a
# manifest on disk, a submission over the protocol. Those are the surfaces
# where "that input cannot happen" has always been wrong eventually.
#
# Usage: scripts/fuzz.sh [seconds]   (default 30)
set -euo pipefail
cd "$(dirname "$0")/.."

SECONDS_PER_TARGET="${1:-30}"

# Discovered rather than listed: a new FuzzXxx is fuzzed by the gate the
# moment it is written, with nothing to remember to update.
targets="$(grep -rn '^func Fuzz' --include='*_test.go' . |
  sed -E 's#^(\./)?(.+)/[^/]+_test\.go:[0-9]+:func (Fuzz[A-Za-z0-9_]*).*#\2 \3#' |
  sort -u)"

[ -n "$targets" ] || { echo "fuzz: no fuzz targets found" >&2; exit 1; }

count=0
while read -r dir target; do
  # A target that matched nothing would "pass" in silence, which is the
  # one outcome a fuzz gate must never produce.
  case "$dir" in
    ""|*[!a-zA-Z0-9_/.-]*) echo "fuzz: unusable package $dir" >&2; exit 1 ;;
  esac
  echo "--- fuzz ${dir} ${target} for ${SECONDS_PER_TARGET}s"
  go test "./$dir" -run '^$' -fuzz "^${target}\$" -fuzztime "${SECONDS_PER_TARGET}s"
  count=$((count + 1))
done <<EOF
$targets
EOF

echo "fuzz: $count target(s) survived ${SECONDS_PER_TARGET}s each"
