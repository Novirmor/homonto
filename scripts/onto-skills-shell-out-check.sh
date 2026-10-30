#!/usr/bin/env bash
# Enforces workflow-skills-shell-out: the onto* and to* SKILL.md files must
# drive every state mutation through their binary (`onto` / `to`) — never a
# direct state-file write — and must not carry the retired "markdown-only /
# no external CLI" copy.
#
# Deliberately coarse (like spec-command-check.sh): a curated blocklist of
# mutation phrasings, metric-write references, and the no-CLI copy. It guards
# the "a skill hand-edits state.yaml" regression class, not full semantics.
# Scope is the workflow SKILL.md files only; references/ (which DOCUMENTS the
# schema, e.g. onto/references/state-yaml.md) and the no-slop skills are
# excluded.
set -euo pipefail
cd "$(dirname "$0")/.."

FILES=(
  catalog/skills/onto/SKILL.md
  catalog/skills/onto-open/SKILL.md
  catalog/skills/onto-design/SKILL.md
  catalog/skills/onto-build/SKILL.md
  catalog/skills/onto-verify/SKILL.md
  catalog/skills/onto-close/SKILL.md
  catalog/skills/onto-light/SKILL.md
  catalog/skills/to/SKILL.md
  catalog/skills/to-plan/SKILL.md
  catalog/skills/to-do/SKILL.md
  catalog/skills/to-done/SKILL.md
)

# 1. The retired no-CLI / markdown-only copy (any of these substrings).
NOCLI='markdown-only|no scripts and no external|are the machinery'

# 2. Metric-write references (all removed by the rewrite).
METRICS='metrics\.(phases|verify_rounds|upgraded)'

# 3. Direct state-file mutation phrasings: a mutation cue on the same line as a
#    state-file token. The abandon `abandoned:` field (no setter, N2) is not a
#    cue and is intentionally not matched.
MUTATE='(set|write|record|stamp|mirror|flip|reset|fill|filled|initialize[d]?|phase advanced|advanced:)[^\n]*(state\.yaml|onto-state\.yaml)|(state\.yaml|onto-state\.yaml)[^\n]*(phase advanced|verify\.result|close\.merged: true|decisions:|guides:)'

fails=0
for f in "${FILES[@]}"; do
  if hits="$(grep -nEi "$NOCLI" "$f")"; then
    printf 'FAIL: %s contains retired no-CLI/markdown-only copy:\n%s\n' "$f" "$hits"
    fails=$((fails + 1))
  fi
  if hits="$(grep -nE "$METRICS" "$f")"; then
    printf 'FAIL: %s contains a metric-write reference (metrics are dropped):\n%s\n' "$f" "$hits"
    fails=$((fails + 1))
  fi
  if hits="$(grep -nEi "$MUTATE" "$f")"; then
    printf 'FAIL: %s contains a direct state-file write instruction:\n%s\n' "$f" "$hits"
    fails=$((fails + 1))
  fi
done

if [ "$fails" -gt 0 ]; then
  echo
  echo "onto-skills-shell-out-check FAILED: $fails finding(s)."
  echo "Route every state mutation through the onto binary; keep schema docs in references/."
  exit 1
fi
echo "onto-skills-shell-out-check passed: onto* SKILL.md files shell out for all state writes."
