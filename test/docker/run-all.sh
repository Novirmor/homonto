#!/bin/sh
# Runs all triple-binary Docker E2E suites against disposable state, printing
# per-suite output and a final PASS/FAIL summary. Exits non-zero if any suite
# fails. Invoked as the image ENTRYPOINT by scripts/docker-test.sh (and CI).
set -u
DIR="$(cd "$(dirname "$0")" && pwd)"
SUITES="homonto-core homonto-expanded multi-repo onto-lifecycle to-lifecycle release-packaging v015-surface"

summary=""
fails=0
for s in $SUITES; do
  printf '\n########## SUITE: %s ##########\n' "$s"
  if sh "$DIR/$s.sh"; then
    summary="${summary}  PASS  ${s}\n"
  else
    summary="${summary}  FAIL  ${s}\n"
    fails=$((fails + 1))
  fi
done

printf '\n================ DOCKER E2E SUMMARY ================\n'
printf '%b' "$summary"
if [ "$fails" -gt 0 ]; then
  printf '\n%d suite(s) FAILED\n' "$fails"
  exit 1
fi
printf '\nALL SUITES PASS\n'
