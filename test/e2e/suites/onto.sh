#!/usr/bin/env bash
# Suite: onto — the standalone onto binary's full spec-driven workflow, end to
# end, against a real materialized framework install. homonto applies
# [frameworks.onto] (the gate onto init/new/advance/close require), then onto
# drives a change from open through close and archives it. Tool-independent, no
# account/network needed.
set -uo pipefail
source /opt/e2e-suites/lib.sh

WORK="$(mktemp -d)"; cd "$WORK"
git init -q
git config user.email "e2e@example.com"
git config user.name "e2e"

log "homonto apply installs the onto framework"
# Every framework-expanded subagent must declare a model in its
# [subagents.<n>.opencode] block (there are no tiers — OpenCode is the only
# adapter). Materialization of .homonto/catalog/skills/onto is
# target-independent, so the onto gate is satisfied either way.
cat > homonto.toml <<'EOF'
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
EOF
homonto apply --yes
[ -d "$WORK/.homonto/catalog/skills/onto" ] || fail "onto framework not materialized to .homonto/catalog/skills/onto"
pass "onto framework materialized (init/new/advance/close gate satisfied)"

log "onto version + doctor (read-only, pre-init)"
# cobra writes to stderr, so capture 2>&1 rather than piping stdout.
ver="$(onto version 2>&1)"; printf '%s\n' "$ver"
contains "$ver" "onto" "onto version did not print a version"
pass "onto version"
# doctor before init: docs layout is missing, so it must REPORT findings and exit non-zero.
if onto doctor >/tmp/doc0.out 2>&1; then
  fail "onto doctor should exit non-zero before the workspace is initialized"
fi
grep -qi 'docs' /tmp/doc0.out || fail "onto doctor did not report the missing docs layout"
pass "onto doctor flags the un-initialized workspace"

log "onto init scaffolds the workspace"
onto init
for d in changes specs adr guides; do
  [ -d "$WORK/docs/$d" ] || fail "onto init did not create docs/$d"
done
pass "onto init created docs/{changes,specs,adr,guides}"
git add -A && git commit -q -m "e2e baseline"
BASE_REF="$(git rev-parse HEAD)"
BASE_BRANCH="$(git branch --show-current)"

log "onto new creates an open-phase change"
onto new e2e-change
onto set base-ref e2e-change "$BASE_REF"
onto set base-branch e2e-change "$BASE_BRANCH"
CH="$WORK/docs/changes/e2e-change"
[ -f "$CH/onto-state.yaml" ] && [ -f "$CH/proposal.md" ] && [ ! -f "$CH/tasks.md" ] \
  || fail "onto new did not create the open-phase skeleton"
grep -q 'phase: open' "$CH/onto-state.yaml" || fail "new change is not at phase open"
pass "onto new → open-phase skeleton"

log "advance open → design"
onto set proposal-approved e2e-change "proposal matches e2e request"
onto advance e2e-change
grep -q 'phase: design' "$CH/onto-state.yaml" || fail "did not advance to design"
pass "open → design"

# Produce each phase's deliverable, then advance out of that phase.
printf '# Design\n\nStatus: Confirmed\n\ne2e design doc\n' > "$CH/design.md"
printf -- '- [x] implement e2e change\n' > "$CH/tasks.md"
onto set approach-confirmed e2e-change "selected e2e approach"
onto set isolation e2e-change branch
log "advance design → build"
onto advance e2e-change
grep -q 'phase: build' "$CH/onto-state.yaml" || fail "did not advance to build"
pass "design → build"

printf '# Plan\ne2e plan\n' > "$CH/plan.md"
# build → verify requires a plan AND every tasks.md checkbox checked (>=1, none unchecked).
log "advance build → verify (plan present, all tasks checked)"
onto advance e2e-change
grep -q 'phase: verify' "$CH/onto-state.yaml" || fail "did not advance to verify"
pass "build → verify"

printf '# Verification\n\nResult: pass\n' > "$CH/verification.md"
onto set verify-result e2e-change pass
# verify → close is release-critical: a dirty worktree BLOCKS it, so commit first.
git add -A && git commit -q -m "e2e-change artifacts"
log "advance verify → close (clean worktree required)"
onto advance e2e-change
grep -q 'phase: close' "$CH/onto-state.yaml" || fail "did not advance to close"
pass "verify → close"

# Record close preparation, including the binary-owned spec merge marker.
onto set guides e2e-change updated
onto set integration e2e-change merge
onto set close-confirmed e2e-change "validated e2e close plan"
onto merge-deltas e2e-change
# The advance and close preparation rewrote state, so the tree is dirty again.
git add -A && git commit -q -m "e2e-change enters close phase"
log "onto close archives the change"
# Close captures integration anchors from a real change branch; the receipt
# below must be a genuine --no-ff merge into the base branch.
git checkout -q -b change/e2e-change
onto close e2e-change
ARCH="$(find "$WORK/docs/changes/archive" -maxdepth 1 -name '*-e2e-change' -type d | head -1)"
[ -n "$ARCH" ] || fail "onto close did not create an archive directory"
grep -q 'archived: true' "$ARCH/onto-state.yaml" || fail "archived change not marked archived: true"
[ ! -d "$CH" ] || fail "onto close left the active change directory behind"
pass "change archived to ${ARCH#$WORK/}"

log "onto doctor is healthy after a clean close"
git add -A && git commit -q -m "archive e2e-change" || true
git checkout -q "$BASE_BRANCH"
git merge --no-ff -q -m "merge change/e2e-change" change/e2e-change
onto complete-integration e2e-change --receipt "merge:$(git rev-parse HEAD)"
git add -A && git commit -q -m "record e2e-change integration"
onto doctor 2>&1 | tee /tmp/doc1.out
grep -qi 'healthy' /tmp/doc1.out || fail "onto doctor is not healthy after close"
pass "onto doctor healthy"

printf '\nSUITE PASS: onto workflow (open→design→build→verify→close→archive→integrate)\n'
