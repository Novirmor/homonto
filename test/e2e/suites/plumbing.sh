#!/usr/bin/env bash
# Suite: plumbing — the credential-free half of the process, interrupt and
# recovery cells, run with no account and no network (the matrix starts it
# with --network none). It proves the machinery those cells assert with, so
# a broken shim or filter fails here instead of reading as a model failure:
#   - the observation shims log what they pass through and stay transparent;
#   - the boundary hold blocks `to new <change>` after the change is recorded;
#   - homonto's real hook answers parse with the suites' jq filters: the
#     resume SessionStart carries the policy and the snapshot at plan, and
#     the policy hook allows the coordinator's `to` commands;
#   - OpenCode 2 runs the projected /to command through session.command (the
#     prompt is the expanded command file), its workflow plugin takes a
#     snapshot for the session, and the session survives a killed service.
# No model ever answers here: OpenCode's model request fails offline.
set -uo pipefail
source /opt/e2e-suites/lib.sh

LOGS="$(mktemp -d)"
install_shims "$LOGS"
workspace_init to
pass "workspace with the to engine applied and committed"
CH=e2e-plumbing

scenario plumbing/shims
to new "$CH" >/dev/null || fail "to new through the shim"
to_ran new "$CH" || fail "the to shim did not log to new $CH"
[ "$(to_phase "$CH")" = plan ] || fail "to status does not show $CH at plan"
homonto workflow snapshot --json --config "$PWD/homonto.toml" >/dev/null || fail "snapshot through the shim"
[ -n "$(shimlog homonto.jsonl 'select(.kind == "snapshot" and .rc == 0) | .out | fromjson | .changes[] | select(.name == "e2e-plumbing")')" ] \
  || fail "the homonto shim did not log the snapshot output"
pass "the shims log to and snapshot calls and pass them through"; scenario_pass

scenario plumbing/claude-hooks
hook() { # <json-input>
  printf '%s' "$1" | homonto runtime hook --config "$PWD/homonto.toml" --agents-dir "$PWD/.claude/agents" --coordinator homonto
}
hook '{"session_id":"plumb-1","hook_event_name":"SessionStart","source":"resume","agent_type":"homonto"}' >/dev/null \
  || fail "the SessionStart hook failed"
[ -n "$(shimlog homonto.jsonl "select(.kind == \"hook\" and .input.hook_event_name == \"SessionStart\" and .input.source == \"resume\")
    | $HOOK_CONTEXT | select(test(\"Homonto execution continuity\"))
    | split(\"\n\")[] | fromjson? | select(.name? == \$c and .phase? == \"plan\")" --arg c "$CH")" ] \
  || fail "the resume SessionStart output does not parse as policy plus a snapshot of $CH at plan"
hook "$(jq -cn --arg c "$CH" --arg d "$PWD" '{session_id:"plumb-1", hook_event_name:"PreToolUse", agent_type:"homonto", tool_name:"Bash", cwd:$d, tool_input:{command:("to phase " + $c)}}')" >/dev/null \
  || fail "the PreToolUse hook failed"
[ -n "$(shimlog homonto.jsonl "select(.kind == \"hook\" and .input.hook_event_name == \"PreToolUse\" and (.input.tool_input.command | test(\"(^|[;&|( ])to phase \")) and $HOOK_DECISION == \"allow\")")" ] \
  || fail "the policy hook did not allow the coordinator's to phase"
pass "homonto's hook output parses with the suites' filters"; scenario_pass

scenario plumbing/boundary
export E2E_HANG_TO_NEW=e2e-held
to new e2e-held >/dev/null 2>&1 &
held=$!
for _ in $(seq 1 20); do [ -s "$LOGS/boundary" ] && break; sleep 0.5; done
[ -s "$LOGS/boundary" ] || fail "the boundary hold never engaged"
kill -0 "$held" 2>/dev/null || fail "to new e2e-held returned instead of holding"
[ "$(to_phase e2e-held)" = plan ] || fail "the held change is not recorded at plan"
kill -KILL "$(cat "$LOGS/boundary")" 2>/dev/null
wait "$held" 2>/dev/null
unset E2E_HANG_TO_NEW
to new e2e-after >/dev/null || fail "a later to new was held too"
pass "the boundary hold blocks only the first to new of its change, after recording it"; scenario_pass

scenario plumbing/opencode-command
sid="ses_e2eplumbing$(date +%s)"
oc_service_start
oc_session_create "$sid" homonto || fail "OpenCode did not create the session"
opencode api session.get --param sessionID="$sid" | jq -e --arg d "$PWD" '.data.agent == "homonto" and .data.location.directory == $d' >/dev/null \
  || fail "the session is not the coordinator at the workspace"
since="$(date +%s)"
oc_command "$sid" to "Open a new to change named e2e-cmd." || fail "session.command refused the projected /to command"
oc_wait "$sid" 30 sh -c "opencode api session.message.list --param sessionID=$sid --param type=user | jq -e '.data[]? | select(.text | test(\"three-phase workflow\"))' >/dev/null" \
  || fail "the session prompt is not the expanded .opencode/command/to.md"
oc_wait "$sid" 30 sh -c "jq -e 'select(.kind == \"snapshot\" and .t >= $since)' '$SHIMLOG/homonto.jsonl' >/dev/null" \
  || fail "the workflow plugin took no snapshot for the session"
pass "OpenCode expanded the projected /to command; the plugin observed the workflow"
pkill -KILL -f 'opencode serve' 2>/dev/null || true
sleep 1
pgrep -f 'opencode serve' >/dev/null && fail "the OpenCode service survived the kill"
oc_service_start
opencode api session.get --param sessionID="$sid" | jq -e '.data.id' >/dev/null || fail "the session did not survive the killed service"
pass "the session survives a killed service"; scenario_pass

printf '\nSUITE PASS: plumbing\n'
