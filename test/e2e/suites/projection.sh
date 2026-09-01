#!/usr/bin/env bash
# Suite: projection — homonto projects config into the tool, then the REAL
# OpenCode CLI is asked to read it back (`opencode mcp list`). This proves
# homonto's output is consumed by the actual tool, not just written to disk.
# Parametrized by $E2E_TOOL (opencode, the only adapter since v0.13.0). No
# account/network needed.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"

WORK="$(mktemp -d)"; cd "$WORK"
mkdir -p homonto/skills/e2e-demo
printf -- '---\nname: e2e-demo\ndescription: e2e projection skill\n---\nbody\n' \
  > homonto/skills/e2e-demo/SKILL.md

case "$TOOL" in
  opencode)
    cat > homonto.toml <<'EOF'
[mcps.e2e-probe]
command = ["codegraph", "serve", "--mcp"]
targets = ["opencode"]

[skills.e2e-demo]
source = "local:e2e-demo"
scope = "user"

[settings.opencode]
theme = "opencode-dark"
EOF
    log "homonto apply → opencode"
    homonto apply --yes
    log "opencode mcp list reads homonto's opencode.jsonc"
    out="$(opencode mcp list 2>&1 || true)"; printf '%s\n' "$out"
    contains "$out" "e2e-probe" "opencode mcp list did not show the homonto-projected server"
    pass "opencode read the projected MCP server from opencode.jsonc"
    grep -q 'opencode-dark' "$HOME/.config/opencode/opencode.jsonc" || fail "opencode setting not projected"
    pass "opencode setting projected"
    link="$HOME/.config/opencode/skills/e2e-demo"
    [ -L "$link" ] || fail "opencode skill symlink not created"
    pass "opencode skill symlinked to the owned source"
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
