#!/bin/sh
# Suite: multi-repo — the ADR 0024 stage-2 contract against real binaries: a
# config repo declares a sibling repository, repo-tagged project resources
# project into the sibling's own files, untagged ones stay in the config repo,
# an UNDECLARED sibling is never touched, and each repository records its own
# state partition. The final isolation assertions are the suite's reason to
# exist: a plan may never write outside the config repo and its declared repos.
set -eu

SUITE=multi-repo
. "$(dirname "$0")/lib.sh"

log "disposable home + config repo with two sibling git worktrees"
HOME="$(mktemp -d)"
export HOME
BASE="$(mktemp -d)"
CFG="$BASE/cfg"
SVC="$BASE/svc"
OTHER="$BASE/other"
mkdir -p "$CFG/homonto/skills/cfg-skill" "$CFG/homonto/skills/svc-skill" "$SVC" "$OTHER"
for r in "$SVC" "$OTHER"; do
  mkdir -p "$r/.git"
done

printf -- '---\nname: cfg-skill\ndescription: config repo skill\n---\nbody\n' > "$CFG/homonto/skills/cfg-skill/SKILL.md"
printf -- '---\nname: svc-skill\ndescription: svc repo skill\n---\nbody\n' > "$CFG/homonto/skills/svc-skill/SKILL.md"

cat > "$CFG/homonto.toml" <<EOF
[repos]
svc = "../svc"

[skills.cfg-skill]
source = "builtin:handoff"
scope = "project"

[skills.svc-skill]
source = "builtin:handoff"
scope = "project"
repo = "svc"
EOF

log "plan names both targets and discloses the config-repo scope"
OUT="$("$HOMONTO" plan --config "$CFG/homonto.toml" 2>&1)" || fail "plan failed: $OUT"
in_file_plan() { printf '%s' "$OUT" | grep -q -- "$1" || fail "plan output must contain '$1'"; }
in_file_plan "opencode@svc"
in_file_plan "svc-skill"
in_file_plan "cfg-skill"
ok "plan carries per-repo changesets and names"

log "apply projects into the declared repo, never the undeclared one"
"$HOMONTO" apply --yes --config "$CFG/homonto.toml" > /dev/null 2>&1 || fail "apply failed"

is_link "$SVC/.opencode/skills/svc-skill"
if [ -e "$SVC/.opencode/skills/cfg-skill" ]; then
  fail "svc repo received the config repo's skill"
fi
is_link "$CFG/.opencode/skills/cfg-skill"
if [ -e "$OTHER/.opencode" ] || [ -e "$OTHER/opencode.jsonc" ]; then
  fail "UNDECLARED repo $OTHER was touched by apply — isolation broken"
fi
ok "declared repo projected; undeclared sibling untouched"
is_file "$CFG/.homonto/state.svc.json"
ok "per-repo state partition written"

log "re-apply is idempotent across both repositories"
OUT2="$("$HOMONTO" apply --yes --config "$CFG/homonto.toml" 2>&1)" || fail "second apply failed"
if printf '%s' "$OUT2" | grep -qE '^(\+|~|-) '; then
  fail "second apply planned changes — not idempotent: $OUT2"
fi
ok "second apply is a no-op"

log "status attributes drift per repository"
rm "$SVC/.opencode/skills/svc-skill" && ln -s "$(mktemp -d)" "$SVC/.opencode/skills/svc-skill"
OUT3="$("$HOMONTO" status --config "$CFG/homonto.toml" 2>&1)" || true
printf '%s' "$OUT3" | grep -q "opencode@svc" || fail "status must attribute svc drift to opencode@svc: $OUT3"
ok "drift finding names the repository"

printf '\nSUITE PASS: %s\n' "$SUITE"
