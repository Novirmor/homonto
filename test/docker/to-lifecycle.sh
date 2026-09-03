#!/bin/sh
# Suite: to-lifecycle — the to binary end to end against a real materialized
# framework install: the framework-install gate, init, new, the single phase
# advance, the required-but-self-asserted --verified flag, abandon, archive,
# the config-free read-only commands, and the onto-xor-to exclusivity error.
set -eu
SUITE=to-lifecycle
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"

log "framework-install gate: to init refuses before homonto apply"
cat > homonto.toml <<'EOF'
[frameworks.to]
source = "builtin:to"
scope = "project"

# Every framework-expanded subagent needs an explicit model in its
# [subagents.<n>.opencode] block (there are no tiers). The primary dispatcher
# `to` needs one too.
[subagents.to.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
EOF
if "$TO" init >/dev/null 2>&1; then fail "to init must refuse before the framework is applied"; fi
absent "$W/docs"
ok "to init refused and created no docs/ tree"

log "read-only commands are config-independent even before apply"
"$TO" status >/dev/null || fail "to status must work without an applied framework"
ok "to status answered without the framework"

log "onto and to are mutually exclusive in one config"
cp homonto.toml /tmp/to-only.toml
printf '\n[frameworks.onto]\nsource = "builtin:onto"\nscope = "project"\n' >> homonto.toml
if "$HOMONTO" plan >/dev/null 2>&1; then fail "homonto must refuse a config declaring both onto and to"; fi
cp /tmp/to-only.toml homonto.toml
ok "homonto refused the onto+to config"

log "homonto apply installs the to framework"
"$HOMONTO" apply --yes >/dev/null
is_dir "$W/.homonto/catalog/skills/to"
is_file "$W/.homonto/catalog/skills/homonto/SKILL.md"
is_file "$W/.homonto/catalog/subagents/to.md"
is_file "$W/.homonto/catalog/subagents/to-skeptic.md"
TVAR="$W/.homonto/catalog/subagents/to.opencode.md"
in_file "$TVAR" 'mode: primary'
in_file "$TVAR" 'steps: 1200'
in_file "$TVAR" '"to-reviewer": allow'
is_link "$W/.opencode/agent/to.md"
ok "framework materialized (skills + subagents)"

log "tooling reference is generated for the to dispatcher"
TOOLING="$W/.homonto/catalog/skills/to/references/tooling.md"
is_file "$TOOLING"
# No [tooling] table declared: the reference must name no third-party tool.
for provider in rtk graphify okf; do
  if grep -qi -- "$provider" "$TOOLING"; then
    fail "default tooling reference must not name $provider"
  fi
done
absent "$W/.homonto/catalog/skills/to-do/references/tooling.md"
ok "default reference names no provider; non-dispatcher has none"

log "editing [tooling] re-renders the to reference"
cat >> homonto.toml <<'EOF'

[tooling]
code_intel = "graphify"
EOF
"$HOMONTO" apply --yes >/dev/null
in_file "$TOOLING" "graphify"
if grep -qi -- "okf" "$TOOLING"; then
  fail "undeclared provider okf leaked into the reference"
fi
ok "reference follows the declared provider"

log "to init scaffolds docs/tasks + archive"
"$TO" init >/dev/null
is_dir "$W/docs/tasks"; is_dir "$W/docs/tasks/archive"
ok "docs/tasks and docs/tasks/archive created"

log "to new creates a plan-phase change with an empty plan.md"
"$TO" new feat-a >/dev/null
CH="$W/docs/tasks/feat-a"
is_file "$CH/to-state.yaml"; is_file "$CH/plan.md"
in_file "$CH/to-state.yaml" 'phase: plan'
ok "plan-phase skeleton created"

log "done refuses from plan and without --verified; phase advances plan -> do"
if "$TO" done feat-a --verified >/dev/null 2>&1; then fail "done must refuse from plan"; fi
"$TO" phase feat-a >/dev/null
in_file "$CH/to-state.yaml" 'phase: do'
if "$TO" done feat-a >/dev/null 2>&1; then fail "done must refuse without --verified"; fi
if "$TO" phase feat-a >/dev/null 2>&1; then fail "phase must refuse from do (done is the only exit)"; fi
ok "the one legal advance ran; done gated on --verified"

log "handoff prints the recovery pack"
printf '# plan\n- [ ] step\n' > "$CH/plan.md"
# CLI output goes to stderr (documented caveat) — fold it for the grep.
"$TO" handoff feat-a 2>&1 | grep -q 'phase: do' || fail "handoff must report the phase"
ok "handoff reported phase + plan"

log "done --verified --evidence archives under a date prefix; the name frees up"
"$TO" done feat-a --verified --evidence "e2e: verify command passed" >/dev/null
ARCH="$(find "$W/docs/tasks/archive" -maxdepth 1 -name '*-feat-a' -type d | head -1)"
[ -n "$ARCH" ] || fail "feat-a was not archived under a date-prefixed dir"
absent "$CH"
in_file "$ARCH/to-state.yaml" 'phase: done'
in_file "$ARCH/to-state.yaml" 'verified: true'
in_file "$ARCH/to-state.yaml" 'e2e: verify command passed'
if "$TO" phase feat-a >/dev/null 2>&1; then fail "phase must refuse on an archived change"; fi
# Date-prefixed archives free the name: a recurring chore can run again.
"$TO" new feat-a >/dev/null
"$TO" abandon feat-a >/dev/null
ok "feat-a done + evidence recorded; name reusable"

log "scoped change records declared repos and refuses done while one is dirty"
git init >/dev/null
git config user.email e2e@example.com
git config user.name E2E
git add -A && git commit -m "workflow baseline" >/dev/null
SVC="$(mktemp -d)"
git -C "$SVC" init >/dev/null
git -C "$SVC" config user.email e2e@example.com
git -C "$SVC" config user.name E2E
printf 'baseline\n' > "$SVC/tracked"
git -C "$SVC" add -A && git -C "$SVC" commit -m init >/dev/null
printf '\n[repos]\nservice = "%s"\n' "$SVC" >> homonto.toml
git add homonto.toml && git commit -m "declare service" >/dev/null
"$TO" new coordinated --repo service >/dev/null
in_file "$W/docs/tasks/coordinated/to-state.yaml" 'repos:'
in_file "$W/docs/tasks/coordinated/to-state.yaml" 'service'
[ ! -e "$SVC/docs/tasks" ] || fail "scoped workflow files must stay in config repo"
git add -A && git commit -m "open coordinated change" >/dev/null
"$TO" phase coordinated >/dev/null
git add -A && git commit -m "enter coordinated do" >/dev/null
printf 'dirty\n' > "$SVC/dirty"
if "$TO" done coordinated --verified >/dev/null 2>&1; then fail "dirty selected repo must block to done"; fi
git -C "$SVC" add -A && git -C "$SVC" commit -m clean >/dev/null
"$TO" done coordinated --verified >/dev/null
[ -n "$(find "$W/docs/tasks/archive" -maxdepth 1 -name '*-coordinated' -type d)" ] || fail "clean scoped change was not archived"
ok "scoped state stays designated and selected repo dirt gates done"

log "abandon is the terminal exit without done"
"$TO" new feat-b >/dev/null
"$TO" abandon feat-b >/dev/null
[ -n "$(find "$W/docs/tasks/archive" -maxdepth 1 -name '*-feat-b' -type d)" ] || fail "feat-b was not archived"
ok "feat-b abandoned and archived"

log "doctor: healthy workspace, wedge finding, quiet contract, convergence"
"$TO" doctor >/dev/null || fail "doctor must be healthy after clean finishes"
"$TO" new feat-c >/dev/null
# Simulate a crash between the terminal state write and the archive rename.
printf 'change: feat-c\nphase: done\nverified: true\nfinished: "2026-01-01"\n' > "$W/docs/tasks/feat-c/to-state.yaml"
if "$TO" doctor >/dev/null 2>&1; then fail "doctor must report the wedged change"; fi
QUIET_OUT="$("$TO" doctor --quiet 2>&1)" && fail "doctor --quiet must exit non-zero on findings"
[ -z "$QUIET_OUT" ] || fail "doctor --quiet must print nothing, got: $QUIET_OUT"
"$TO" done feat-c --verified >/dev/null   # converges the interrupted archive
[ -n "$(find "$W/docs/tasks/archive" -maxdepth 1 -name '*-feat-c' -type d)" ] || fail "convergence did not archive feat-c"
"$TO" doctor >/dev/null || fail "doctor must be healthy after convergence"
ok "doctor found the wedge, stayed quiet with --quiet, and done converged it"

log "status --json lists nothing active after both archives"
"$TO" status --json 2>&1 | grep -q '^\[\]' || fail "status --json should be an empty array"
ok "active listing empty"

printf '\nSUITE PASS: %s\n' "$SUITE"
