#!/usr/bin/env bash
# Suite: projection — homonto projects config into the tool alongside a
# bring-your-own MCP server the user wrote by hand, then the REAL OpenCode CLI
# is asked to read it back (`opencode mcp list`). This proves homonto's output
# coexists with, and never rewrites, the user's own MCP configuration.
# Parametrized by $E2E_TOOL (opencode, the only adapter since v0.13.0). No
# account/network needed.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"

WORK="$(mktemp -d)"; cd "$WORK"

case "$TOOL" in
  opencode)
    mkdir -p "$HOME/.config/opencode/skills/own-skill"
    printf -- '---\nname: own-skill\ndescription: bring-your-own skill\n---\nbody\n' \
      > "$HOME/.config/opencode/skills/own-skill/SKILL.md"
    printf '{"mcp":{"e2e-probe":{"type":"local","command":["codegraph","serve","--mcp"],"enabled":true}}}\n' \
      > "$HOME/.config/opencode/opencode.jsonc"
    cat > homonto.toml <<'EOF'
[skills.e2e-demo]
source = "builtin:handoff"
scope = "user"

[settings.opencode]
theme = "opencode-dark"
EOF
    log "homonto apply → opencode"
    homonto apply --yes
    log "opencode mcp list still reads the user's own server"
    out="$(opencode mcp list 2>&1 || true)"; printf '%s\n' "$out"
    contains "$out" "e2e-probe" "opencode mcp list lost the bring-your-own server"
    pass "the bring-your-own MCP server survived homonto's projection"
    grep -q 'opencode-dark' "$HOME/.config/opencode/opencode.jsonc" || fail "opencode setting not projected"
    pass "opencode setting projected"
    link="$HOME/.config/opencode/skills/e2e-demo"
    [ -L "$link" ] || fail "opencode skill symlink not created"
    pass "opencode skill symlinked to the catalog source"
    [ -f "$HOME/.config/opencode/skills/own-skill/SKILL.md" ] && [ ! -L "$HOME/.config/opencode/skills/own-skill" ] \
      || fail "apply touched the bring-your-own skill"
    pass "the bring-your-own skill was left alone"
    ;;
  *) fail "unknown E2E_TOOL: $TOOL" ;;
esac

log "second apply is idempotent"
out="$(homonto apply --yes 2>&1)"; printf '%s\n' "$out"
contains "$out" "No changes" "second apply was not idempotent"
pass "re-apply is a no-op"

log "homonto doctor confirms the links"
homonto doctor 2>&1 | tee /tmp/doctor.out
grep -q 'e2e-demo' /tmp/doctor.out || fail "doctor did not mention the owned skill"
pass "doctor healthy for the projected skill"

printf '\nSUITE PASS: projection/%s\n' "$TOOL"
