#!/usr/bin/env bash
# Suite: recovery — on the REAL harness, an active onto change reaches the
# coordinator through homonto's runtime integration at session start, after
# resuming the session, and after compaction (ADR 0064, ADR 0103). It also
# calls the runtime's status tool from inside the session. Uses the mounted
# account: Claude Code with haiku, OpenCode with a free model.
#
# Scenarios (stable IDs, recorded as E2E-EVIDENCE lines):
#   recovery/claude/start        the change is in the context at startup
#   recovery/claude/resume       ... after --resume
#   recovery/claude/compaction   ... after /compact
#   recovery/claude/status-tool  homonto_status answers over MCP
#   recovery/opencode/start      a new session sees the change
#   recovery/opencode/continue   a continued session still sees it
#   recovery/opencode/compaction after a real compaction the session's context
#                                holds only the compaction summary, yet the
#                                next request carries a snapshot taken after
#                                it (a change created after compaction is
#                                named) and the homonto_status tool reports it
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"

LOGS="$(mktemp -d)"
install_shims "$LOGS"

WORK="$(mktemp -d)"; cd "$WORK" || exit 1
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
T=300   # hard per-invocation timeout (seconds)

case "$TOOL" in
  claude)
    [ -f "$HOME/.claude/.credentials.json" ] || fail "claude credentials not mounted"
    claude_trust "$(pwd)"
    scenario recovery/claude/start
    sid="$(uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())')"
    out="$(timeout "$T" claude -p --session-id "$sid" --model haiku --max-turns 3 "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the session did not see the change at start"
    pass "startup context"; scenario_pass
    scenario recovery/claude/resume
    out="$(timeout "$T" claude -p --resume "$sid" --model haiku --max-turns 3 "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the resumed session lost the change"
    pass "resume context"; scenario_pass
    scenario recovery/claude/compaction
    since="$(date +%s)"
    timeout "$T" claude -p --resume "$sid" --model haiku "/compact" >/dev/null 2>&1 || true
    out="$(timeout "$T" claude -p --resume "$sid" --model haiku --max-turns 3 "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the change was lost after compaction"
    # The hook output proves re-injection; a resume alone is not proof
    # that /compact actually ran.
    compact="$(shimlog homonto.jsonl 'select(.kind == "hook" and .t >= $s and .input.session_id == $sid and .input.hook_event_name == "SessionStart" and .input.source == "compact" and (.out | test("recovery-probe")) and (.out | test("Homonto execution continuity")))' --argjson s "$since" --arg sid "$sid")"
    [ -n "$compact" ] || fail "no compact SessionStart re-injected the policy and the snapshot for this session"
    pass "compaction context (policy and snapshot re-injected by compact SessionStart)"; scenario_pass
    scenario recovery/claude/status-tool
    out="$(timeout "$T" claude -p --model haiku --max-turns 3 --allowedTools=mcp__homonto__homonto_status \
      'Call the mcp__homonto__homonto_status tool once and reply with the value of the "name" field of its first change.' 2>&1)"
    printf '%s\n' "$out"
    contains "$out" "recovery-probe" "homonto_status did not answer from the Claude Code session"
    pass "runtime tool over MCP"; scenario_pass
    ;;
  opencode)
    [ -f "$HOME/.local/share/opencode/auth.json" ] || fail "opencode auth not mounted"
    MODEL="${E2E_OPENCODE_MODEL:-opencode/deepseek-v4.1-flash-free}"
    sid="ses_e2erecovery$(date +%s)"
    trap 'rc=$?; [ $rc -ne 0 ] && oc_dump "$sid"' EXIT
    scenario recovery/opencode/start
    out="$(timeout "$T" opencode run -s "$sid" -m "$MODEL" "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the OpenCode session did not see the change"
    pass "session context"; scenario_pass
    scenario recovery/opencode/continue
    out="$(timeout "$T" opencode run -m "$MODEL" --continue "$ASK" 2>&1)"; printf '%s\n' "$out"
    contains "$out" "recovery-probe" "the continued session lost the change"
    pass "continued-session context"; scenario_pass

    scenario recovery/opencode/compaction
    # Compact the session through OpenCode's own API (the TUI's /compact).
    opencode api session.compact --param sessionID="$sid" -d '{}' || fail "OpenCode refused the compaction request"
    compacted() { oc_messages "$sid" compaction | jq -e '.data[]? | select(.status == "completed" or .status == "failed")' >/dev/null 2>&1; }
    oc_wait "$sid" "$T" compacted || fail "the compaction did not finish within ${T}s"
    status="$(oc_messages "$sid" compaction | jq -r '[.data[]? | .status] | last')"
    [ "$status" = completed ] || fail "the compaction ended $status"
    # The active context now starts at the compaction: the earlier prompts are gone.
    users="$(opencode api session.context --param sessionID="$sid" | jq '[.data[]? | select(.type == "user")] | length')"
    [ "$users" = 0 ] || fail "the session context still holds $users pre-compaction prompt(s)"
    pass "OpenCode compacted the session (status completed, no earlier prompt in context)"
    # A change created after the compaction exists only in a fresh snapshot,
    # never in the summary or the transcript, so naming it proves the next
    # request carried the restored workflow context.
    late="late-$(date +%s | tail -c 6)"
    onto new "$late" >/dev/null || fail "onto new $late"
    since="$(date +%s)"
    out="$(timeout "$T" opencode run -s "$sid" -m "$MODEL" \
      'Answer from the homonto workflow context only, with no tools: list the names of every active workflow change, comma-separated, and nothing else.' 2>&1)"
    printf '%s\n' "$out"
    contains "$out" "$late" "after compaction the session did not see the current workflow snapshot"
    contains "$out" "recovery-probe" "after compaction the session lost the original change"
    [ -n "$(shimlog homonto.jsonl "select(.kind == \"snapshot\" and .t >= $since and (.out | test(\"$late\")))")" ] \
      || fail "no workflow snapshot naming $late was taken for the post-compaction request"
    pass "the post-compaction request carried a fresh snapshot (policy and snapshot are injected together, v2.ts inject)"
    # The handoff/status tool reads the same restored state from the session.
    since="$(date +%s)000"
    # The runtime tools belong to the coordinator, so this turn runs as it.
    timeout "$T" opencode run -s "$sid" --agent homonto -m "$MODEL" \
      'Call the homonto_status tool exactly once, then reply with just the word DONE.' >/dev/null 2>&1 || true
    oc_messages "$sid" | jq -e --argjson s "$since" --arg late "$late" '[.data[]? | select(.type == "assistant" and .time.created >= $s)
        | (.content // [])[] | select(.type == "tool" and .name == "homonto_status" and .state.status == "completed")
        | (.state.content | tostring) | select(test($late))] | length > 0' >/dev/null \
      || fail "homonto_status did not report the current changes after compaction"
    pass "homonto_status reports the current changes after compaction"; scenario_pass
    ;;
  *) fail "unknown E2E_TOOL: $TOOL" ;;
esac

printf '\nSUITE PASS: recovery/%s\n' "$TOOL"
