#!/usr/bin/env bash
# Suite: recovery — on the REAL harness, an active onto change reaches the
# coordinator through homonto's runtime integration at session start, after
# resuming the session, and after compaction (ADR 0064, ADR 0103). It also
# calls the runtime's status tool from inside the session. Uses the mounted
# account: Claude Code with haiku, OpenCode with a free model.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"

WORK="$(mktemp -d)"; cd "$WORK"
git init -q . && git -c user.email=e2e@x -c user.name=e2e commit -q --allow-empty -m init
cat > homonto.toml <<'TOML'
harnesses = ["opencode", "claude"]

[engines.onto]
source = "builtin:onto"
scope = "project"

[routing.default.opencode]
model = "opencode/deepseek-v4.1-flash-free"

[routing.default.claude]
model = "haiku"
TOML
homonto apply --yes >/dev/null || fail "apply"
onto init >/dev/null 2>&1 || true
onto new recovery-probe >/dev/null || fail "onto new"
pass "an active onto change exists"
ASK='Answer from the homonto workflow context only, with no tools: what is the name of the active workflow change? Reply with just the name.'

case "$TOOL" in
  claude)
    [ -f "$HOME/.claude/.credentials.json" ] || fail "claude credentials not mounted"
    claude_trust "$(pwd)"
    log "session start injects the change"
    sid="$(uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())')"
    out="$(claude -p --session-id "$sid" --model haiku "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the session did not see the change at start"
    pass "startup context"
    log "a resumed session still sees it"
    out="$(claude -p --resume "$sid" --model haiku "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the resumed session lost the change"
    pass "resume context"
    log "after compaction the policy and snapshot are re-injected"
    claude -p --resume "$sid" --model haiku "/compact" >/dev/null 2>&1 || true
    out="$(claude -p --resume "$sid" --model haiku "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the change was lost after compaction"
    pass "compaction context"
    log "the coordinator calls the runtime status tool"
    out="$(claude -p --model haiku --allowedTools=mcp__homonto__homonto_status \
      'Call the mcp__homonto__homonto_status tool once and reply with the value of the "name" field of its first change.' 2>&1)"
    printf '%s\n' "$out"
    contains "$out" "recovery-probe" "homonto_status did not answer from the Claude Code session"
    pass "runtime tool over MCP"
    ;;
  opencode)
    [ -f "$HOME/.local/share/opencode/auth.json" ] || fail "opencode auth not mounted"
    MODEL="${E2E_OPENCODE_MODEL:-opencode/deepseek-v4.1-flash-free}"
    log "a new session sees the change"
    out="$(opencode run -m "$MODEL" "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the OpenCode session did not see the change"
    pass "session context"
    log "a continued session still sees it"
    out="$(opencode run -m "$MODEL" --continue "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the continued session lost the change"
    pass "continued-session context"
    ;;
  *) fail "unknown E2E_TOOL: $TOOL" ;;
esac

printf '\nSUITE PASS: recovery/%s\n' "$TOOL"
