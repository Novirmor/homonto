#!/usr/bin/env bash
# Suite: interrupt — a builtin process is killed inside the REAL harness at a
# defined state boundary and resumed (#71). The session runs the projected
# /to command; the `to` observation shim holds the harness's `to new` tool
# call open once the change is recorded, so the kill always lands at the same
# state: the change exists at phase plan and the model has not seen the
# result. The whole harness is then killed (SIGKILL: the claude process group,
# or OpenCode's background service), a fresh process resumes the same session
# with a prompt that names no change, and the suite checks that the restored
# policy and snapshot reached the resumed run and that the run continued from
# the recorded state.
#
# Scenarios (stable IDs, recorded as E2E-EVIDENCE lines):
#   interrupt/<tool>/boundary  the harness was killed while the process was at
#                              the boundary: change recorded at plan, harness
#                              gone, nothing advanced
#   interrupt/<tool>/restore   the resumed run received the policy and a fresh
#                              snapshot naming the change at plan (Claude Code:
#                              the resume SessionStart hook's output; OpenCode:
#                              the workflow snapshot its context hook took)
#   interrupt/<tool>/resume    the resumed run advanced that change with
#                              `to phase` to do, without re-creating it
# Uses the mounted account: Claude Code with haiku (bounded turns), OpenCode
# with a free model. Each harness run has a hard timeout.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"
T="${E2E_PROCESS_TIMEOUT:-900}"
CH=e2e-interrupt
ARGS="Open a new to change named $CH for this work: add a file NOTES.md containing the word hello. Endpoint: stop as soon as $CH is in phase do. Do not implement, verify, commit or finish it, and do not ask questions."
RESUME="The previous run was interrupted. Recover the active to change from its recorded state and continue it. Endpoint: stop as soon as it is in phase do. Do not implement, verify, commit or finish it, and do not ask questions."

LOGS="$(mktemp -d)"
install_shims "$LOGS"
export E2E_HANG_TO_NEW="$CH"
workspace_init to
pass "workspace with the to engine applied and committed"

# wait_boundary <pid>: until the shim holds `to new` open, or the run ends.
wait_boundary() {
  local deadline=$(( $(date +%s) + T ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    [ -s "$LOGS/boundary" ] && return 0
    if [ -n "${1:-}" ] && ! kill -0 "$1" 2>/dev/null; then return 1; fi
    sleep 1
  done
  return 1
}
release_boundary() { kill -KILL "$(cat "$LOGS/boundary" 2>/dev/null)" 2>/dev/null || true; }

case "$TOOL" in
  claude)
    [ -f "$HOME/.claude/.credentials.json" ] || fail "claude credentials not mounted"
    claude_trust "$(pwd)"
    trap 'rc=$?; release_boundary; if [ $rc -ne 0 ]; then printf "\n--- hook log ---\n"; shimlog homonto.jsonl "select(.kind == \"hook\") | {e:.input.hook_event_name, src:.input.source, agent:.input.agent_type, cmd:(.input.tool_input.command // null), out:(.out[0:160])}"; printf -- "--- to calls ---\n"; cat "$LOGS/to.jsonl" 2>/dev/null; fi' EXIT
    sid="$(python3 -c 'import uuid;print(uuid.uuid4())')"

    scenario interrupt/claude/boundary
    setsid timeout "$T" claude -p --session-id "$sid" --model haiku --max-turns 40 --permission-mode acceptEdits \
      "/to $ARGS" >"$LOGS/session1.txt" 2>&1 &
    pid=$!
    wait_boundary "$pid" || { tail -20 "$LOGS/session1.txt"; fail "the session never reached the boundary (to new $CH)"; }
    kill -KILL -- "-$pid" 2>/dev/null || true
    release_boundary
    pkill -KILL -f 'homonto runtime' 2>/dev/null || true
    wait "$pid" 2>/dev/null
    { kill -0 "$pid" 2>/dev/null || pgrep -f -- "--session-id $sid" >/dev/null; } && fail "claude survived the kill"
    [ "$(to_phase "$CH")" = plan ] || fail "at the boundary $CH is not at plan: '$(to_phase "$CH")'"
    to_ran phase "$CH" && fail "the change advanced before the kill; the boundary was not held"
    pass "killed at the boundary: $CH recorded at plan, harness gone"; scenario_pass

    unset E2E_HANG_TO_NEW
    since="$(date +%s)"
    timeout "$T" claude -p --resume "$sid" --model haiku --max-turns 40 --permission-mode acceptEdits \
      "$RESUME" >"$LOGS/session2.txt" 2>&1 || true
    tail -30 "$LOGS/session2.txt"

    scenario interrupt/claude/restore
    [ -n "$(shimlog homonto.jsonl "select(.kind == \"hook\" and .t >= \$s and .input.hook_event_name == \"SessionStart\" and .input.source == \"resume\")
        | $HOOK_CONTEXT | select(test(\"Homonto execution continuity\"))
        | split(\"\n\")[] | fromjson? | select(.name? == \$c and .phase? == \"plan\")" --argjson s "$since" --arg c "$CH")" ] \
      || fail "the resume SessionStart hook did not inject the policy and a snapshot of $CH at plan"
    pass "the resumed session started with the policy and the snapshot of $CH at plan"; scenario_pass
    ;;
  opencode)
    [ -f "$HOME/.local/share/opencode/auth.json" ] || fail "opencode auth not mounted"
    sid="ses_e2einterrupt$(date +%s)"
    trap 'rc=$?; release_boundary; if [ $rc -ne 0 ]; then oc_dump "$sid"; printf -- "--- to calls ---\n"; cat "$LOGS/to.jsonl" 2>/dev/null; fi' EXIT

    scenario interrupt/opencode/boundary
    oc_service_start
    oc_session_create "$sid" homonto || fail "OpenCode did not create the session"
    oc_command "$sid" to "$ARGS" || fail "OpenCode did not run the projected /to command"
    wait_boundary || fail "the session never reached the boundary (to new $CH) within ${T}s"
    # The session runs inside the background service: killing it is the crash.
    pkill -KILL -f 'opencode serve' 2>/dev/null || true
    release_boundary
    pkill -KILL -f 'homonto runtime' 2>/dev/null || true
    sleep 1
    pgrep -f 'opencode serve' >/dev/null && fail "the OpenCode service survived the kill"
    [ "$(to_phase "$CH")" = plan ] || fail "at the boundary $CH is not at plan: '$(to_phase "$CH")'"
    to_ran phase "$CH" && fail "the change advanced before the kill; the boundary was not held"
    pass "killed at the boundary: $CH recorded at plan, service gone"; scenario_pass

    unset E2E_HANG_TO_NEW
    since="$(date +%s)"
    oc_service_start
    timeout "$T" opencode run -s "$sid" --agent homonto -m "$OC_MODEL" "$RESUME" >"$LOGS/session2.txt" 2>&1 || true
    tail -30 "$LOGS/session2.txt"

    scenario interrupt/opencode/restore
    # The plugin's context hook takes a snapshot for every model request and
    # injects it with the continuation policy (v2.ts inject); the first one
    # after the restart must name the change at its recorded phase.
    [ -n "$(shimlog homonto.jsonl "select(.kind == \"snapshot\" and .t >= $since and .rc == 0) | .out | fromjson | .changes[] | select(.name == \"$CH\" and .phase == \"plan\")")" ] \
      || fail "no snapshot of $CH at plan was taken for the resumed run"
    pass "the resumed run's context hook observed $CH at plan"; scenario_pass
    ;;
  *) fail "unknown E2E_TOOL: $TOOL" ;;
esac

scenario "interrupt/$TOOL/resume"
to_ran phase "$CH" "$since" || fail "the resumed run did not advance $CH with to phase"
[ "$(to_phase "$CH")" = "do" ] || fail "after the resumed run $CH is at '$(to_phase "$CH")', not do"
[ "$(find docs/tasks -maxdepth 1 -name "$CH*" | wc -l)" = 1 ] || fail "the resumed run created a second $CH"
pass "the resumed run continued $CH from plan to do"; scenario_pass

printf '\nSUITE PASS: interrupt/%s\n' "$TOOL"
