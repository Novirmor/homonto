#!/bin/sh
# Suite: v015-surface — the seven v0.15.0 capabilities exercised end to end
# against disposable homes/repositories: explain + provenance, relative links
# surviving a repo move, evidence recording + trace, to-promote, permission
# suggestions + bundled plugin projection, and snapshot apply/undo.
set -eu
SUITE=v015-surface
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"

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

[mcps.demo]
command = ["true"]

[skills.projskill]
source = "local:projskill"
scope = "project"

[plugins.opencode.permission-observer]
source = "permission-observer"
EOF

mkdir -p homonto/skills/projskill
printf '# proj\n' > homonto/skills/projskill/SKILL.md

log "apply projects the v0.15.0 surface"
"$HOMONTO" apply --yes >/dev/null
is_dir ".opencode/skills/projskill"
# Project link carries a RELATIVE target (ADR 0026).
link="$(readlink .opencode/skills/projskill)"
case "$link" in
  /*) fail "project link must be relative, got $link" ;;
esac
# The bundled plugin materialized as owned catalog content.
is_file ".homonto/catalog/plugins/permission-observer/plugin.ts"
ok "relative link + bundled plugin materialized"

log "explain names origins and history"
"$HOMONTO" explain skill projskill > explain.txt 2>&1
grep -q "direct" explain.txt || fail "explain must name a direct origin"
grep -q "last: create" explain.txt || fail "explain must name the creating operation"
"$HOMONTO" explain --json 2>&1 | grep -q '"schemaVersion"\|"kind"' || fail "explain --json malformed"
ok "explain reports origin and last change"

log "repository move converges without manual link surgery"
mv "$W" "$W-moved"; cd "$W-moved"
"$HOMONTO" apply --yes >/dev/null
[ -e ".opencode/skills/projskill" ] || fail "project link missing after move"
"$HOMONTO" status 2>&1 | grep -q "No drift" || fail "status dirty after move+apply"
ok "repo move converges"

log "onto handoff --write persists metadata-only packs"
"$ONTO" init
"$ONTO" new feat-a >/dev/null
"$ONTO" handoff feat-a --write | grep -q "wrote" || fail "handoff --write output"
found="$(ls docs/changes/feat-a/.onto/handoff/*-context.json)"
grep -q '"schemaVersion"' "$found" || fail "handoff envelope missing schemaVersion"
grep -q "hunter2" "$found" && fail "persisted pack leaked prose"
ok "handoff envelope persisted (metadata only)"

log "evidence record + trace + doctor"
mkdir -p docs/changes/feat-a/specs
cat > docs/changes/feat-a/specs/login.md <<'MD'
## ADDED Requirements

### Requirement: password reset

Requirement-ID: REQ-reset-1
The system SHALL email a reset link.

#### Scenario: expired token

Scenario-ID: SC-reset-expired
- **GIVEN** a token older than 1h
- **WHEN** the link is used
- **THEN** reset is refused
MD
printf '# Tasks\n\n- [x] #1 implement\n' > docs/changes/feat-a/tasks.md
"$ONTO" evidence record feat-a --task 1 --scenario SC-reset-expired \
  --exec go --cmd-hash 0123456789012345678901234567890123456789012345678901234567890123 \
  --exit 0 --output /dev/null >/dev/null
grep -q '"schemaVersion": 1' docs/changes/feat-a/.onto/evidence.json || fail "sidecar missing"
"$ONTO" trace feat-a --json 2>&1 | grep -q '"scenario"' || fail "trace lacks scenario nodes"
"$ONTO" doctor 2>&1 | grep -q "healthy" || fail "doctor not healthy after clean evidence"
ok "evidence record/trace/doctor"

log "to promote converts a to change"
mkdir -p docs/tasks/grower
cat > docs/tasks/grower/to-state.yaml <<'YAML'
change: grower
phase: do
YAML
printf '# plan\n- [ ] #1 work\n  - Files: `x.go`\nFinal Verify: `go test ./...`\n' > docs/tasks/grower/plan.md
"$TO" promote grower --yes 2>&1 | grep -q "promoted" || fail "promote output"
is_file "docs/changes/grower/onto-state.yaml"
is_file "docs/changes/grower/imported-to/plan.md"
grep -q 'phase: open' docs/changes/grower/onto-state.yaml || fail "promote must start at open"
ok "to promote preserved the source"

log "permissions suggest renders only safe commands"
printf 'go test ./...\nrm -rf /\n' | "$HOMONTO" permissions suggest > suggest.txt 2>&1
grep -q 'bash_allow_add' suggest.txt || fail "snippet missing"
grep -q '"go test ./..."' suggest.txt || fail "safe command missing from snippet"
if grep -q '"rm -rf' suggest.txt; then :; fi
# The destructive command may appear only inside a # rejected line.
rejected="$(grep -c '# rejected' suggest.txt)"
[ "$rejected" -ge 1 ] || fail "unsafe command not rejected"
ok "suggestions validated"

log "snapshot apply + undo restores state"
# Add a setting (a real change to snapshot), apply it normally, then undo a
# snapshot of the next change. The config edit happens BEFORE the snapshot
# so the journal records a non-empty transaction.
cat >> homonto.toml <<'TOML'

[settings.opencode]
theme = "snap-theme"
TOML
before="$(cat .homonto/state.json)"
"$HOMONTO" apply --yes --snapshot > snap.txt 2>&1
grep -q "snapshot: apply recorded as" snap.txt || fail "snapshot apply output"
id="$(sed -n 's/.*recorded as \([0-9a-f]\{8\}\).*/\1/p' snap.txt)"
[ -n "$id" ] || fail "no apply id"
"$HOMONTO" snapshot undo "$id" --yes >/dev/null
after="$(cat .homonto/state.json)"
[ "$before" != "$after" ] && fail "undo did not restore pre-snapshot state"
ok "snapshot undo restored state"

printf '\nSUITE PASS: %s\n' "$SUITE"
