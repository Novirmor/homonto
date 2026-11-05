#!/usr/bin/env bash
# Suite: projection — homonto projects config into the tool alongside a
# bring-your-own MCP server the user wrote by hand, then the REAL OpenCode CLI
# is asked to read it back (`opencode debug config`). This proves homonto's output
# coexists with, and never rewrites, the user's own MCP configuration.
# Parametrized by $E2E_TOOL (opencode or claude). No account/network needed.
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
    log "OpenCode's resolved config still carries the user's own server"
    # OpenCode 2's `mcp list` reports a running instance's servers; its
    # resolved configuration is what proves the file still reads cleanly.
    out="$(opencode debug config 2>&1 || true)"; printf '%s\n' "$out" | head -40
    contains "$out" "e2e-probe" "OpenCode lost the bring-your-own server"
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
  claude)
    # Claude Code: the onto engine and the GitHub hosting workflow project
    # natively next to OpenCode, and Claude Code itself reads them back.
    git init -q . && git -c user.email=e2e@x -c user.name=e2e commit -q --allow-empty -m init
    cat > homonto.toml <<'TOML'
harnesses = ["opencode", "claude"]

[engines.onto]
source = "builtin:onto"
scope = "project"

[services.host-gh]
source = "builtin:host-gh"
scope = "project"

[workflows.h-workflow-gh]
source = "builtin:h-workflow-gh"
scope = "project"

[routing.default.opencode]
model = "opencode/deepseek-v4.1-flash-free"

[routing.default.claude]
model = "haiku"

[skills.e2e-demo]
source = "builtin:handoff"
scope = "user"
TOML
    log "homonto apply → opencode + claude"
    homonto apply --yes
    for f in .claude/skills/onto/SKILL.md .claude/commands/onto.md .claude/agents/onto-skeptic.md .claude/agents/homonto.md .opencode/agent/onto-skeptic.md; do
      [ -e "$f" ] || fail "$f not projected"
    done
    pass "skills, commands and agents projected for both harnesses"
    grep -q 'disallowedTools: Edit, Write, MultiEdit, NotebookEdit, Bash' .claude/agents/onto-skeptic.md || fail "skeptic not read-only on Claude Code"
    grep -q 'homonto_policy' .claude/agents/onto-implementer.md || fail "implementer policy not rendered"
    pass "agents rendered natively, read-only and policy-bound"
    jq -e '.hooks.PreToolUse and .hooks.SessionStart and .agent == "homonto" and (.permissions.ask | index("mcp__homonto__homonto_github_publish"))' .claude/settings.json >/dev/null \
      || fail "project settings lack the runtime integration"
    jq -e '.mcpServers.homonto.args[0:2] == ["runtime","mcp"]' .mcp.json >/dev/null || fail ".mcp.json lacks the homonto server"
    pass "hooks, permissions, session agent and MCP server projected"
    [ -L "$HOME/.claude/skills/e2e-demo" ] || fail "user-scope Claude skill not linked"
    pass "user-scope skill linked into ~/.claude/skills"
    log "claude mcp list connects to the homonto runtime once the folder is trusted"
    out="$(claude mcp list 2>&1 || true)"; printf '%s\n' "$out"
    contains "$out" "Pending approval" "an untrusted folder must not run the project's MCP server"
    pass "an untrusted config root does not start the server"
    claude_trust "$(pwd)"
    out="$(claude mcp list 2>&1 || true)"; printf '%s\n' "$out"
    contains "$out" "homonto" "claude does not see the homonto MCP server"
    contains "$out" "Connected" "the homonto MCP server did not connect"
    pass "Claude Code reads .mcp.json and the homonto runtime answers"
    log "homonto doctor reports the Claude Code tier and accepted limitations"
    doc="$(homonto doctor 2>&1)"; printf '%s\n' "$doc"
    contains "$doc" "harness claude 2.1: workflow tier" "doctor lacks the Claude Code tier"
    contains "$doc" "accepted, ADR 0105" "doctor lacks the accepted limitations"
    pass "doctor reports tier and limitations"
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
