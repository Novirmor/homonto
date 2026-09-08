#!/bin/sh
# Suite: schema-2 control, records, sources, and execution roots through real CLIs.
set -eu
SUITE=workspace-layout
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"
export HOME
export GIT_AUTHOR_NAME="Workspace E2E" GIT_AUTHOR_EMAIL="workspace@example.test"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
export GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0
CFG="$HOME/control"
RECORDS="$CFG/records"
WORKTREES="$HOME/execution"
SNAP="$HOME/snapshots"
mkdir -p "$CFG" "$SNAP" "$HOME/api" "$HOME/web" "$HOME/combined"

# Capture both index bytes and logical dirt; status probes must not refresh it.
snapshot_source() {
    git -C "$1" rev-parse HEAD > "$2.head"
    git -C "$1" symbolic-ref HEAD > "$2.branch"
    git -C "$1" status --porcelain=v1 -z --untracked-files=all > "$2.status"
    git -C "$1" diff --binary > "$2.diff"
    git -C "$1" diff --cached --binary > "$2.staged"
    cp "$1/.git/index" "$2.index"
    cp "$1/app.sh" "$2.app"
    cp "$1/untracked note" "$2.untracked"
}

assert_checkpoint() {
    [ "$(git -C "$RECORDS" log -1 --format=%s)" = "$1" ] || fail "missing automatic $1 checkpoint"
    [ "$(git -C "$RECORDS" rev-parse HEAD^)" = "$HISTORY_HEAD" ] || fail "$1 must create exactly one checkpoint"
    [ -z "$(git -C "$RECORDS" status --porcelain)" ] || fail "$1 left uncommitted records"
    HISTORY_HEAD="$(git -C "$RECORDS" rev-parse HEAD)"
}

log "real sources on different base branches, isolated fixture identity"
for repo in api web; do
    base=main
    [ "$repo" != web ] || base=develop
    git -C "$HOME/$repo" init -q -b "$base"
    printf '#!/bin/sh\nprintf "%%s\\n" "%s baseline"\n' "$repo" > "$HOME/$repo/app.sh"
    git -C "$HOME/$repo" add app.sh
    git -C "$HOME/$repo" commit -q -m "initial $repo"
done

cat > "$HOME/framework.toml" <<'EOF'
[frameworks.h]
source = "builtin:h"
scope = "project"
[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.h-spike.opencode]
model = "openai/gpt-5-mini"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
EOF
cat > "$CFG/homonto.toml" <<'EOF'
schema_version = 2
[workflow]
root = "records"
git = "managed"
[worktrees]
dir = "../execution"
[repos]
api = "../api"
web = "../web"
EOF
cat "$HOME/framework.toml" >> "$CFG/homonto.toml"
cd "$CFG"

log "apply h and inspect without implicitly initializing history"
"$HOMONTO" apply --yes > "$SNAP/apply.log" 2>&1 || { cat "$SNAP/apply.log" >&2; fail "apply failed"; }
is_dir "$CFG/.homonto/catalog/skills/onto"
is_dir "$CFG/.homonto/catalog/skills/to"
"$HOMONTO" workspace inspect --json > "$SNAP/before-init.json"
in_file "$SNAP/before-init.json" '"initialized":false'
absent "$RECORDS"
absent "$CFG/.git"
"$HOMONTO" workspace init --yes
is_dir "$RECORDS/.git"
is_file "$RECORDS/.homonto-workflow.json"
[ "$(git -C "$RECORDS" rev-list --count HEAD)" = 1 ] || fail "init must create the initial history commit"
HISTORY_HEAD="$(git -C "$RECORDS" rev-parse HEAD)"
"$ONTO" init
"$TO" init
for dir in changes specs adr guides tasks tasks/archive; do is_dir "$RECORDS/$dir"; done
[ "$(git -C "$RECORDS" rev-parse HEAD)" = "$HISTORY_HEAD" ] || fail "empty-directory init created false history"
absent "$CFG/docs"
ok "managed records initialized; control remains non-Git"

log "open explicit source scope and preserve staged, unstaged, and untracked originals"
"$TO" new cross --repo api --repo web
assert_checkpoint "to new"
STATE="$RECORDS/tasks/cross/to-state.yaml"
in_file "$STATE" 'repo_mode: explicit'
for repo in api web; do
    in_file "$STATE" "- $repo"
    in_file "$STATE" "git_common_dir: $HOME/$repo/.git"
    printf '#!/bin/sh\nprintf "%%s\\n" "%s staged original"\n' "$repo" > "$HOME/$repo/app.sh"
    git -C "$HOME/$repo" add app.sh
    printf '# unstaged original\n' >> "$HOME/$repo/app.sh"
    printf '%s\000untracked\n' "$repo" > "$HOME/$repo/untracked note"
    snapshot_source "$HOME/$repo" "$SNAP/$repo-before"
    [ -s "$SNAP/$repo-before.diff" ] || fail "missing unstaged fixture dirt"
    [ -s "$SNAP/$repo-before.staged" ] || fail "missing staged fixture dirt"
    base=main
    [ "$repo" != web ] || base=develop
    "$HOMONTO" worktree create cross --workflow to --repo "$repo" --base "$base" --branch work/cross --json > "$SNAP/$repo-binding.json"
    execution="$WORKTREES/$repo/to-cross"
    is_file "$execution/.git"
    grep -Fq "\"path\":\"$execution\"" "$SNAP/$repo-binding.json" || fail "wrong $repo execution path"
    grep -Fq "\"baseRef\":\"$base\"" "$SNAP/$repo-binding.json" || fail "wrong $repo base ref"
    head="$(git -C "$HOME/$repo" rev-parse HEAD)"
    grep -Fq "\"baseCommit\":\"$head\"" "$SNAP/$repo-binding.json" || fail "base commit not pinned"
    [ "$(git -C "$execution" rev-parse HEAD)" = "$head" ] || fail "execution did not start at $base"
    [ "$(sh "$execution/app.sh")" = "$repo baseline" ] || fail "original dirt leaked into execution"
done
ok "registered worktrees start clean at main and develop"

log "inspect/list/handoff resolve paths without committing manual record edits"
cp "$RECORDS/tasks/cross/plan.md" "$SNAP/plan-before"
printf '# manual plan edit, not a checkpoint\n' > "$RECORDS/tasks/cross/plan.md"
cp "$RECORDS/tasks/cross/plan.md" "$SNAP/plan-dirty"
cp "$RECORDS/.git/index" "$SNAP/records.index"
git -C "$RECORDS" status --porcelain -z > "$SNAP/records-before.status"
"$HOMONTO" workspace inspect --json > "$SNAP/inspect.json"
for pair in "config_root:$CFG" "workflow_root:$RECORDS" "worktrees_dir:$WORKTREES" "api:$HOME/api" "web:$HOME/web"; do
    grep -Fq "\"${pair%%:*}\":\"${pair#*:}\"" "$SNAP/inspect.json" || fail "inspect lost $pair"
done
in_file "$SNAP/inspect.json" '"initialized":true'
in_file "$SNAP/inspect.json" '"pending":false'
"$HOMONTO" worktree list --json > "$SNAP/list.json"
"$TO" handoff cross --json > "$SNAP/handoff.json" 2>&1 || { cat "$SNAP/handoff.json" >&2; fail "handoff failed"; }
"$TO" status --json > "$SNAP/to-status.json" 2>&1 || { cat "$SNAP/to-status.json" >&2; fail "to status failed"; }
"$ONTO" status
for repo in api web; do
    grep -Fq "\"path\":\"$WORKTREES/$repo/to-cross\"" "$SNAP/list.json" || fail "list lost $repo binding"
    grep -Fq "$WORKTREES/$repo/to-cross" "$SNAP/handoff.json" || fail "handoff lost $repo execution context"
done
[ "$(git -C "$RECORDS" rev-parse HEAD)" = "$HISTORY_HEAD" ] || fail "read-only commands created a commit"
git -C "$RECORDS" status --porcelain -z > "$SNAP/records-after.status"
cmp "$SNAP/records-before.status" "$SNAP/records-after.status" || fail "inspection changed record dirt"
cmp "$SNAP/records.index" "$RECORDS/.git/index" || fail "inspection changed record index"
cmp "$SNAP/plan-dirty" "$RECORDS/tasks/cross/plan.md" || fail "inspection changed plan bytes"
cp "$SNAP/plan-before" "$RECORDS/tasks/cross/plan.md"
"$TO" phase cross
assert_checkpoint "to phase"
in_file "$STATE" 'phase: do'
cp "$STATE" "$SNAP/state-before-done"
ok "read-only commands preserve history and manual edits; phase auto-checkpoints"

log "corrupt registry refuses rather than falling back to original repositories"
REGISTRY="$CFG/.homonto/worktrees.json"
cp "$REGISTRY" "$SNAP/registry.json"
printf '{bad registry\n' > "$REGISTRY"
if "$HOMONTO" worktree list --json > "$SNAP/corrupt-list.log" 2>&1; then fail "list accepted corrupt registry"; fi
in_file "$SNAP/corrupt-list.log" 'invalid registry'
if "$TO" done cross --verified --evidence "must not archive" > "$SNAP/corrupt-done.log" 2>&1; then fail "done accepted corrupt registry"; fi
in_file "$SNAP/corrupt-done.log" 'invalid registry'
cmp "$SNAP/state-before-done" "$STATE" || fail "corrupt-registry refusal mutated state"
[ "$(git -C "$RECORDS" rev-parse HEAD)" = "$HISTORY_HEAD" ] || fail "registry refusal created false history"
cp "$SNAP/registry.json" "$REGISTRY"
ok "registry corruption blocks list and completion without state/history changes"

log "dirty execution blocks completion, then verified code commits permit archival"
for repo in api web; do
    printf '#!/bin/sh\nprintf "%%s\\n" "%s cross"\n' "$repo" > "$WORKTREES/$repo/to-cross/app.sh"
done
if "$TO" done cross --verified --evidence "must not archive" > "$SNAP/dirty-done.log" 2>&1; then fail "dirty execution allowed done"; fi
for text in api web app.sh 'preserve or isolate'; do in_file "$SNAP/dirty-done.log" "$text"; done
cmp "$SNAP/state-before-done" "$STATE" || fail "dirty refusal mutated state"
[ "$(git -C "$RECORDS" rev-parse HEAD)" = "$HISTORY_HEAD" ] || fail "dirty refusal created false history"
for repo in api web; do
    execution="$WORKTREES/$repo/to-cross"
    [ "$(sh "$execution/app.sh")" = "$repo cross" ] || fail "$repo code verification failed"
    git -C "$execution" diff --check
    git -C "$execution" add app.sh
    git -C "$execution" commit -q -m "implement cross in $repo"
    [ "$(git -C "$execution" rev-parse HEAD^)" = "$(git -C "$HOME/$repo" rev-parse HEAD)" ] || fail "$repo implementation commit has wrong parent"
    [ -z "$(git -C "$execution" status --porcelain)" ] || fail "$repo execution is not clean"
done
EVIDENCE='sh execution/{api,web}/to-cross/app.sh: api cross / web cross; git diff --check: exit 0 in both worktrees'
"$TO" done cross --verified --evidence "$EVIDENCE"
assert_checkpoint "to done"
absent "$RECORDS/tasks/cross"
set -- "$RECORDS"/tasks/archive/*-cross
[ "$#" = 1 ] || fail "expected one cross archive"
ARCH="$1"
is_file "$ARCH/to-state.yaml"
in_file "$ARCH/to-state.yaml" 'phase: done'
in_file "$ARCH/to-state.yaml" 'verified: true'
in_file "$ARCH/to-state.yaml" 'repo_mode: explicit'
grep -Fq "$EVIDENCE" "$ARCH/to-state.yaml" || fail "archive lost evidence"
git -C "$RECORDS" show "HEAD:${ARCH#"$RECORDS/"}/to-state.yaml" > "$SNAP/committed-state"
cmp "$SNAP/committed-state" "$ARCH/to-state.yaml" || fail "archive not committed automatically"
ok "dirty execution refused; committed code and evidence archived with automatic history"

log "source HEAD, branch, index, and all dirty bytes remain unchanged"
for repo in api web; do
    snapshot_source "$HOME/$repo" "$SNAP/$repo-after"
    for part in head branch status diff staged index app untracked; do
        cmp "$SNAP/$repo-before.$part" "$SNAP/$repo-after.$part" || fail "$repo original $part changed"
    done
    absent "$HOME/$repo/docs"
    absent "$HOME/$repo/records"
    absent "$WORKTREES/$repo/to-cross/docs"
    absent "$WORKTREES/$repo/to-cross/records"
done
absent "$CFG/.git"
absent "$CFG/docs"
ok "only execution branches and managed records advanced"

log "combined existing-Git schema-2 layout explicitly selects repos.app = '.'"
COMBINED="$HOME/combined"
git -C "$COMBINED" init -q -b main
printf 'combined baseline\n' > "$COMBINED/source.txt"
git -C "$COMBINED" add source.txt
git -C "$COMBINED" commit -q -m "combined baseline"
COMBINED_HEAD="$(git -C "$COMBINED" rev-parse HEAD)"
cat > "$COMBINED/homonto.toml" <<'EOF'
schema_version = 2
[workflow]
root = "docs"
git = "existing"
[repos]
app = "."
EOF
cat "$HOME/framework.toml" >> "$COMBINED/homonto.toml"
cd "$COMBINED"
"$HOMONTO" apply --yes > "$SNAP/combined-apply.log" 2>&1 || { cat "$SNAP/combined-apply.log" >&2; fail "combined apply failed"; }
"$HOMONTO" workspace init --yes
"$ONTO" init
"$TO" init
"$ONTO" new scoped-onto --repo app
"$TO" new scoped-to --repo app
for state in "$COMBINED/docs/changes/scoped-onto/onto-state.yaml" "$COMBINED/docs/tasks/scoped-to/to-state.yaml"; do
    is_file "$state"
    in_file "$state" 'repo_mode: explicit'
    in_file "$state" '- app'
    in_file "$state" "git_common_dir: $COMBINED/.git"
done
in_file "$COMBINED/docs/changes/scoped-onto/onto-state.yaml" 'base_branch: main'
"$HOMONTO" workspace inspect --json > "$SNAP/combined-inspect.json"
in_file "$SNAP/combined-inspect.json" '"git_mode":"existing"'
grep -Fq "\"workflow_root\":\"$COMBINED/docs\"" "$SNAP/combined-inspect.json" || fail "combined records resolved incorrectly"
grep -Fq "\"app\":\"$COMBINED\"" "$SNAP/combined-inspect.json" || fail "explicit dot source resolved incorrectly"
[ "$(git -C "$COMBINED" rev-parse HEAD)" = "$COMBINED_HEAD" ] || fail "existing mode auto-committed source repo"
absent "$COMBINED/docs/.git"
absent "$COMBINED/docs/.homonto-workflow.json"
ok "both CLIs scaffold explicit combined scope without taking over existing Git history"

printf '\nSUITE PASS: %s\n' "$SUITE"
