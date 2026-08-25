#!/usr/bin/env bash
# Decide whether a release may happen at all.
#
# The gate proves the code works. This proves the RELEASE was prepared:
# that it can be signed, that it can be published, and that the three
# things which only ever matter when something goes wrong — the live host
# matrix, the schema migration, and the rollback — were actually exercised
# rather than assumed.
#
# Those three are the ones that get waved through. A host cell nobody could
# run (no macOS to hand, no OpenCode installed) reads exactly like a host
# cell that passed, unless something refuses. This refuses.
#
# Usage: scripts/release-guard.sh [--evidence <dir>]
#
# Evidence directory (default: dist/evidence):
#   host-cells.tsv            <host> TAB <platform> TAB <result>, one per line;
#                             every host/platform cell must read exactly "pass"
#   migration-rehearsal.txt   what was migrated, from which schema, and how it
#                             was checked — non-empty
#   rollback-rehearsal.txt    a failed activation and the exact rollback that
#                             followed — non-empty
#
# Environment:
#   HOMONTO_RELEASE_SIGNING_KEY  path to the Ed25519 private key
#   HOMONTO_RELEASE_ROOT_ID      the root id to sign as
#   GH_TOKEN                     credentials to publish with
set -uo pipefail
cd "$(dirname "$0")/.."

EVIDENCE="dist/evidence"
while [ $# -gt 0 ]; do
  case "$1" in
    --evidence) EVIDENCE="${2:?--evidence needs a directory}"; shift 2 ;;
    *) echo "release-guard: unknown argument $1" >&2; exit 2 ;;
  esac
done

HOSTS="claude opencode"
PLATFORMS="linux darwin"

missing=""
refuse() { missing="${missing}  - $1"$'\n'; }

# --- signing --------------------------------------------------------------
key="${HOMONTO_RELEASE_SIGNING_KEY:-}"
if [ -z "$key" ]; then
  refuse "HOMONTO_RELEASE_SIGNING_KEY is not set: an unsigned release is one no binary carrying a signing root will install"
elif [ ! -f "$key" ]; then
  refuse "HOMONTO_RELEASE_SIGNING_KEY points at $key, which does not exist"
fi
[ -n "${HOMONTO_RELEASE_ROOT_ID:-}" ] || \
  refuse "HOMONTO_RELEASE_ROOT_ID is not set: a signature has to name the root it was made by"

# --- credentials ----------------------------------------------------------
[ -n "${GH_TOKEN:-}" ] || \
  refuse "GH_TOKEN is not set: the release cannot be published"

# --- live host matrix -----------------------------------------------------
cells="$EVIDENCE/host-cells.tsv"
if [ ! -f "$cells" ]; then
  refuse "no $cells: the acceptance matrix is the whole argument that the workflows work against real agents"
else
  for host in $HOSTS; do
    for platform in $PLATFORMS; do
      result="$(awk -F'\t' -v h="$host" -v p="$platform" \
        '$1==h && $2==p {print $3; found=1} END {if (!found) print "<absent>"}' "$cells" | head -n 1)"
      if [ "$result" != "pass" ]; then
        refuse "host cell $host/$platform is \"$result\", not \"pass\" — run it or do not release"
      fi
    done
  done
fi

# --- rehearsals -----------------------------------------------------------
rehearsal() {
  path="$EVIDENCE/$1"
  if [ ! -f "$path" ]; then
    refuse "no $path: $2"
  elif [ ! -s "$path" ]; then
    refuse "$path is empty: $2"
  fi
}
rehearsal migration-rehearsal.txt \
  "a schema migration nobody rehearsed is discovered by the first user who updates"
rehearsal rollback-rehearsal.txt \
  "the rollback path only runs when something has already gone wrong, so it is the one that must be exercised deliberately"

if [ -n "$missing" ]; then
  printf 'release-guard: this release is not ready.\n\n%s\n' "$missing" >&2
  printf 'Every item above is a thing that would otherwise be discovered by a user.\n' >&2
  exit 1
fi

echo "release-guard: signing, credentials, host matrix, and both rehearsals are present."
