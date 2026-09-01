#!/bin/sh
# Suite: onto-lifecycle — the onto binary end to end against a real materialized
# framework install: the framework-install gate, init, new, gated phase advances
# (including a failure gate), doctor, dependency-aware close, and archive.
set -eu
SUITE=onto-lifecycle
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"
git init -q
git config user.email e2e@example.com
git config user.name e2e

log "framework-install gate: onto init refuses before homonto apply"
cat > homonto.toml <<'EOF'
[frameworks.onto]
source = "builtin:onto"
scope = "project"

# Every framework-expanded subagent needs an explicit model in its
# [subagents.<n>.opencode] block (there are no tiers). The primary dispatcher
# `onto` needs one too — it renders for OpenCode like every other agent.
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
if "$ONTO" init >/dev/null 2>&1; then fail "onto init must refuse before the framework is applied"; fi
absent "$W/docs"
ok "onto init refused and created no docs/ tree"

log "homonto apply installs the onto framework"
"$HOMONTO" apply --yes >/dev/null
is_dir "$W/.homonto/catalog/skills/onto"
ok "framework materialized"

log "tooling reference is generated for the dispatcher"
TOOLING="$W/.homonto/catalog/skills/onto/references/tooling.md"
is_file "$TOOLING"
# No [tooling] table declared: both providers default to none, so the generated
# reference must name no third-party tool at all.
for provider in rtk graphify okf; do
  if grep -qi -- "$provider" "$TOOLING"; then
    fail "default tooling reference must not name $provider"
  fi
done
in_file "$TOOLING" "direct file reading"
# A phase skill is not a dispatcher and must not carry the reference.
absent "$W/.homonto/catalog/skills/onto-build/references/tooling.md"
ok "default reference names no provider; non-dispatcher has none"

log "editing [tooling] re-renders the reference"
cat >> homonto.toml <<'EOF'

[tooling]
shell_proxy = "rtk"
code_intel = "okf"
EOF
"$HOMONTO" apply --yes >/dev/null
in_file "$TOOLING" "okf"
in_file "$TOOLING" "rtk"
if grep -qi -- "graphify" "$TOOLING"; then
  fail "undeclared provider graphify leaked into the reference"
fi
ok "reference follows the declared providers"

log "a deleted tooling reference is restored"
rm "$TOOLING"
"$HOMONTO" apply --yes >/dev/null
is_file "$TOOLING"
ok "deleted reference repaired by the next apply"

log "an unknown provider fails at load"
cp homonto.toml homonto.toml.bak
sed -i 's/code_intel = "okf"/code_intel = "ctags"/' homonto.toml
if "$HOMONTO" apply --yes >/dev/null 2>&1; then
  fail "an unknown code_intel provider must fail at load"
fi
mv homonto.toml.bak homonto.toml
"$HOMONTO" apply --yes >/dev/null
ok "unknown provider rejected; config restored"

log "onto init scaffolds the workspace"
"$ONTO" init >/dev/null
for d in changes specs adr guides; do is_dir "$W/docs/$d"; done
ok "docs/{changes,specs,adr,guides} created"

log "onto new creates an open-phase change (full: proposal only, no tasks yet)"
"$ONTO" new feat-a >/dev/null
CH="$W/docs/changes/feat-a"
is_file "$CH/onto-state.yaml"; is_file "$CH/proposal.md"
absent "$CH/tasks.md"   # full derives its task list from the confirmed design
in_file "$CH/onto-state.yaml" 'phase: open'
ok "open-phase skeleton created (no tasks.md)"

log "judgment gate: leaving open refuses until proposal-approved is recorded"
if "$ONTO" advance feat-a >/dev/null 2>&1; then fail "advance must refuse to leave open without proposal-approved"; fi
"$ONTO" set proposal-approved feat-a "2026-07-22 approved" >/dev/null
ok "open exit gated on the proposal-approved evidence token"

log "advance open -> design (needs only proposal), then design exit gates on design.md + tasks.md"
"$ONTO" advance feat-a >/dev/null
in_file "$CH/onto-state.yaml" 'phase: design'
if "$ONTO" advance feat-a >/dev/null 2>&1; then fail "advance must refuse to leave design without design.md"; fi
printf '# Design\n' > "$CH/design.md"
# design.md alone is not enough now: leaving design also needs the derived tasks.md.
if "$ONTO" advance feat-a >/dev/null 2>&1; then fail "advance must refuse to leave design without tasks.md"; fi
in_file "$CH/onto-state.yaml" 'phase: design'
ok "design exit gated on both design.md and the derived tasks.md"

log "derive tasks + produce deliverables, advance design -> build -> verify -> close"
printf -- '- [x] done\n' > "$CH/tasks.md"   # derived from the confirmed design
# Entering build requires a chosen isolation (branch|worktree); the binary
# refuses otherwise, so record it before the design -> build advance.
"$ONTO" set isolation feat-a branch >/dev/null
# The approach gate's token is required too — isolation alone is refused.
if "$ONTO" advance feat-a >/dev/null 2>&1; then fail "advance must refuse design->build without approach-confirmed"; fi
"$ONTO" set approach-confirmed feat-a "2026-07-22 approach" >/dev/null
"$ONTO" advance feat-a >/dev/null; in_file "$CH/onto-state.yaml" 'phase: build'
printf '# Plan\n' > "$CH/plan.md"
"$ONTO" advance feat-a >/dev/null; in_file "$CH/onto-state.yaml" 'phase: verify'
# The verify exit cross-checks the report: verify.result=pass beside a report
# with no "Result: pass" line refuses — the state and the report must agree.
printf '# Verification\nResult: fail\n' > "$CH/verification.md"
"$ONTO" set verify-result feat-a pass >/dev/null
git add -A && git commit -q -m "feat-a artifacts (failing report)"
if "$ONTO" advance feat-a >/dev/null 2>&1; then fail "advance must refuse when verification.md disagrees with verify.result"; fi
printf '# Verification\nResult: pass\n' > "$CH/verification.md"
git add -A && git commit -q -m "feat-a report passes"
"$ONTO" advance feat-a >/dev/null; in_file "$CH/onto-state.yaml" 'phase: close'
ok "verify exit cross-checked verification.md against the recorded result"
# close additionally requires the merged flag and resolved guides (full
# workflow); record them before the commit so the worktree stays clean.
"$ONTO" set close-merged feat-a >/dev/null
"$ONTO" set guides feat-a updated >/dev/null
# The final-confirmation gate's token: close refuses without it.
git add -A && git commit -q -m "feat-a close evidence" >/dev/null 2>&1 || true
if "$ONTO" close feat-a >/dev/null 2>&1; then fail "close must refuse without close-confirmed"; fi
"$ONTO" set close-confirmed feat-a "2026-07-22 confirmed" >/dev/null
git add -A && git commit -q -m "feat-a enters close"
ok "feat-a advanced through every gate to close"

log "dependency-aware close: feat-b depends on the still-active feat-a"
"$ONTO" new feat-b >/dev/null
# Shortcut feat-b straight to the close phase (skip the per-phase advances).
sed 's/^phase: open/phase: close/' "$W/docs/changes/feat-b/onto-state.yaml" > /tmp/fb.yaml
mv /tmp/fb.yaml "$W/docs/changes/feat-b/onto-state.yaml"
# Satisfy every close-evidence gate so the ONLY thing blocking close is the
# unresolved dependency on the still-active feat-a.
"$ONTO" set deps feat-b --dep feat-a >/dev/null
"$ONTO" set verify-result feat-b pass >/dev/null
"$ONTO" set close-merged feat-b >/dev/null
"$ONTO" set guides feat-b updated >/dev/null
"$ONTO" set close-confirmed feat-b "2026-07-22 confirmed" >/dev/null
git add -A && git commit -q -m "feat-b at close depending on feat-a"
if "$ONTO" close feat-b >/dev/null 2>&1; then fail "close must refuse while dependency feat-a is unresolved"; fi
is_dir "$W/docs/changes/feat-b"
ok "close refused with an unresolved dependency"

log "close feat-a (archives it), then feat-b's dependency is satisfied"
"$ONTO" close feat-a >/dev/null
ARCH="$(find "$W/docs/changes/archive" -maxdepth 1 -name '*-feat-a' -type d | head -1)"
[ -n "$ARCH" ] || fail "feat-a was not archived"
in_file "$ARCH/onto-state.yaml" 'archived: true'
absent "$CH"
git add -A && git commit -q -m "archive feat-a"
"$ONTO" close feat-b >/dev/null
[ -n "$(find "$W/docs/changes/archive" -maxdepth 1 -name '*-feat-b' -type d)" ] || fail "feat-b did not archive after its dependency resolved"
ok "feat-a archived; feat-b closed once its dependency resolved"

log "preset (fix) advances mechanically open->build->verify->close (N2 regression)"
git add -A && git commit -q -m "archive feat-b" || true
"$ONTO" new feat-fix --workflow fix >/dev/null
FX="$W/docs/changes/feat-fix"
is_file "$FX/proposal.md"; is_file "$FX/tasks.md"   # presets scaffold tasks at open-lite
printf -- '- [x] reproduce\n- [x] fix\n' > "$FX/tasks.md"
"$ONTO" set isolation feat-fix branch >/dev/null
# Presets are exempt from the full-only judgment tokens and reach build in
# ONE gated call (the former scripted double-advance is gone).
"$ONTO" advance feat-fix --to build >/dev/null; in_file "$FX/onto-state.yaml" 'phase: build'
"$ONTO" advance feat-fix >/dev/null; in_file "$FX/onto-state.yaml" 'phase: verify'
printf '# Verification\nResult: pass\n' > "$FX/verification.md"
"$ONTO" set verify-result feat-fix pass >/dev/null
git add -A && git commit -q -m "feat-fix artifacts"
"$ONTO" advance feat-fix >/dev/null; in_file "$FX/onto-state.yaml" 'phase: close'
"$ONTO" set close-merged feat-fix >/dev/null   # presets need no guides
"$ONTO" set close-confirmed feat-fix "2026-07-22 confirmed" >/dev/null
git add -A && git commit -q -m "feat-fix enters close"
"$ONTO" close feat-fix >/dev/null
[ -n "$(find "$W/docs/changes/archive" -maxdepth 1 -name '*-feat-fix' -type d)" ] || fail "preset did not archive"
ok "preset advanced through every phase mechanically and archived"

log "onto doctor is healthy after clean closes"
git add -A && git commit -q -m "archive feat-fix" || true
"$ONTO" doctor >/dev/null 2>&1 || fail "onto doctor reported problems"
ok "onto doctor healthy"

printf '\nSUITE PASS: %s\n' "$SUITE"
