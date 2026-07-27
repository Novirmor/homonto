#!/usr/bin/env bash
# Keeps the agent-facing docs honest about reality (ADR 0017).
#
# AGENTS.md once mandated a `/graphify` skill that was installed nowhere, and
# pointed at a wiki index that had never been generated. Nothing caught it,
# because nothing checked. This guards that class: every local path the agent
# docs name must exist, and the paths declared untracked must stay untracked.
#
# Deliberately coarse, like spec-command-check.sh was: it verifies EXISTENCE,
# not correctness. It cannot tell you the advice went stale — only that a
# reference points at nothing.
#
# Scope is BOTH the repo's own agent docs (§1–4) and the shipped catalog (§5–7).
# The catalog is the larger body of agent-facing prose by far, and it was
# unchecked until a review pointed out that the guard covered everything except
# the tree it was written to protect.
set -euo pipefail
cd "$(dirname "$0")/.."

# Scope is every tracked markdown doc, not just the agent-facing ones: the
# first version checked AGENTS.md and docs/agents/ only, and promptly missed a
# link to the deleted comet-workflow.md sitting in docs/guides/.
DOCS=(README.md AGENTS.md)
while IFS= read -r f; do DOCS+=("$f"); done < <(find docs -name '*.md' | sort)

fails=0

# 1. Every relative markdown link resolves to a file or directory that exists.
#    Skips external URLs and pure anchors.
for doc in "${DOCS[@]}"; do
  [ -f "$doc" ] || { echo "MISSING doc: $doc"; fails=$((fails + 1)); continue; }
  dir="$(dirname "$doc")"
  while IFS= read -r target; do
    case "$target" in
      http://*|https://*|mailto:*|'#'*) continue ;;
    esac
    target="${target%%#*}"
    [ -n "$target" ] || continue
    if [ ! -e "$dir/$target" ]; then
      echo "$doc: link target does not exist: $target"
      fails=$((fails + 1))
    fi
  done < <(grep -oE '\]\([^)]+\)' "$doc" | sed -E 's/^\]\(//; s/\)$//')
done

# 2. Every `scripts/*.sh` or `.py` mentioned in backticks exists. Catches a doc
#    naming a script that was renamed or retired.
for doc in "${DOCS[@]}"; do
  [ -f "$doc" ] || continue
  while IFS= read -r s; do
    [ -e "$s" ] || { echo "$doc: references missing script: $s"; fails=$((fails + 1)); }
  done < <(grep -oE '`(\./)?scripts/[A-Za-z0-9_.-]+\.sh`' "$doc" \
             | tr -d '`' | sed 's#^\./##' | sort -u)
done

# 3. Paths ADR 0017 declares untracked must not be tracked. If one creeps back
#    in, the "artifacts are scratch" rule has quietly stopped being true.
for p in openspec docs/superpowers okf_bundle .superpowers; do
  if git ls-files --error-unmatch "$p" >/dev/null 2>&1; then
    echo "tracked but must be untracked (ADR 0017): $p"
    fails=$((fails + 1))
  fi
done

# 4. The docs AGENTS.md points at must actually be there.
for p in docs/agents/comet.md docs/agents/okf.md docs/agents/verification.md \
         docs/agents/adr.md docs/adr; do
  [ -e "$p" ] || { echo "missing required doc: $p"; fails=$((fails + 1)); }
done

# --- the shipped catalog ------------------------------------------------
# Skills and subagents are agent-facing docs too, and cross-skill references
# are the same failure mode as the /graphify one: a skill telling an agent to
# read a file that ships nowhere. `references/tooling.md` is the one legitimate
# dangling name — `homonto apply` generates it per install from `[tooling]`.
GENERATED='references/tooling.md'

CATALOG_DOCS=()
while IFS= read -r f; do CATALOG_DOCS+=("$f"); done < <(find catalog -name '*.md' | sort)

# 5. Relative markdown links in catalog docs resolve. Placeholder targets
#    (`<capability>`) are template text, not references — skipped.
for doc in "${CATALOG_DOCS[@]}"; do
  dir="$(dirname "$doc")"
  while IFS= read -r target; do
    case "$target" in
      http://*|https://*|mailto:*|'#'*|*'<'*|*'>'*) continue ;;
    esac
    target="${target%%#*}"
    [ -n "$target" ] || continue
    if [ ! -e "$dir/$target" ]; then
      echo "$doc: link target does not exist: $target"
      fails=$((fails + 1))
    fi
  done < <(grep -oE '\]\([^)]+\)' "$doc" | sed -E 's/^\]\(//; s/\)$//')
done

# 6. Backticked path-like references in catalog docs resolve against one of the
#    three roots a skill can name: its own directory (`references/x.md`), the
#    skills root (`onto-verify/references/x.md`), or the repo (`docs/x.md`).
#    Bare filenames (`plan.md`) are change-workspace artifacts, not repo files.
for doc in "${CATALOG_DOCS[@]}"; do
  dir="$(dirname "$doc")"
  skilldir="$dir"
  case "$dir" in */references) skilldir="$(dirname "$dir")" ;; esac
  while IFS= read -r ref; do
    case "$ref" in */*) ;; *) continue ;; esac
    [ "$ref" = "$GENERATED" ] && continue
    # Adopter-workspace paths: `onto init` / `to init` scaffold these in the
    # repo that adopts the framework. homonto does not dogfood onto, so they
    # are correctly absent here and are not this check's business.
    case "$ref" in docs/changes/*|docs/specs/*|docs/tasks/*) continue ;; esac
    if [ ! -e "$skilldir/$ref" ] && [ ! -e "catalog/skills/$ref" ] && [ ! -e "$ref" ]; then
      echo "$doc: references nothing that ships: $ref"
      fails=$((fails + 1))
    fi
  done < <(grep -oE '`[A-Za-z0-9_./-]+\.(md|sh|py)`' "$doc" | tr -d '`' | sort -u)
done

# 7. Every subagent a skill or subagent declares in `spawn:` actually ships.
#    A dispatch to a name with no definition fails at runtime, not at build.
while IFS= read -r line; do
  doc="${line%%:*}"
  names="${line#*spawn: [}"
  names="${names%]*}"
  [ -n "$names" ] || continue
  for n in ${names//,/ }; do
    [ -e "catalog/subagents/$n.md" ] || {
      echo "$doc: spawns a subagent that does not ship: $n"
      fails=$((fails + 1))
    }
  done
done < <(grep -rn '^  spawn: \[.\+\]' catalog/skills catalog/subagents || true)

if [ "$fails" -ne 0 ]; then
  echo "agents-doc-check: $fails problem(s)" >&2
  exit 1
fi
echo "agents-doc-check: agent docs and catalog reference only things that exist"
