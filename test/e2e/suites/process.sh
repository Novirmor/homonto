#!/usr/bin/env bash
# Suite: process — a builtin engine process run by a model inside the REAL
# harness, through the projected command and skills, not by driving the CLI
# (#71 builtins on both harnesses; on Claude Code also #65, a process run with
# homonto's hooks active). The session is handed the projected /to command
# with a fixed change name and an early endpoint; the to skills then drive
# the binary. Assertions come from the observation shims (what the harness
# ran, what homonto's hooks answered) and from the workflow records, never
# from the model's prose.
#
# Scenarios (stable IDs, recorded as E2E-EVIDENCE lines):
#   process/<tool>/create   the session created the change with `to new`
#                           from inside the harness (shim log), phase plan
#   process/<tool>/advance  the session advanced it with `to phase` to do
#   process/<tool>/status   `to status` and the workflow snapshot agree: one
#                           active change, phase do
#   process/claude/hooks    the run went through homonto's hooks: the
#                           coordinator started with the policy injected,
#                           and the policy hook allowed each to command
#                           as the coordinator
# Uses the mounted account: Claude Code with haiku (bounded turns), OpenCode
# with a free model. Each harness run has a hard timeout.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"
T="${E2E_PROCESS_TIMEOUT:-900}"
CH=e2e-proc
ARGS="Open a new to change named $CH for this work: add a file NOTES.md containing the word hello. Endpoint: stop as soon as $CH is in phase do. Do not implement, verify, commit or finish it, and do not ask questions."

LOGS="$(mktemp -d)"
install_shims "$LOGS"
workspace_init to
pass "workspace with the to engine applied and committed"

case "$TOOL" in
  claude)
    [ -f "$HOME/.claude/.credentials.json" ] || fail "claude credentials not mounted"
    claude_trust "$(pwd)"
    trap 'rc=$?; if [ $rc -ne 0 ]; then printf "\n--- hook log ---\n"; shimlog homonto.jsonl "select(.kind == \"hook\") | {e:.input.hook_event_name, agent:.input.agent_type, tool:.input.tool_name, cmd:(.input.tool_input.command // null), out:(.out[0:120])}"; printf -- "--- to calls ---\n"; cat "$LOGS/to.jsonl" 2>/dev/null; fi' EXIT
    log "claude -p runs the projected /to command as the coordinator"
    sid="$(python3 -c 'import uuid;print(uuid.uuid4())')"
    # acceptEdits lets the coordinator write plan.md in a non-interactive run;
    # shell commands still go through homonto's policy hook.
    timeout "$T" claude -p --session-id "$sid" --model haiku --max-turns 40 --permission-mode acceptEdits \
      "/to $ARGS" >"$LOGS/session.txt" 2>&1 || true
    tail -40 "$LOGS/session.txt"
    ;;
  opencode)
    [ -f "$HOME/.local/share/opencode/auth.json" ] || fail "opencode auth not mounted"
    sid="ses_e2eprocess$(date +%s)"
    trap 'rc=$?; if [ $rc -ne 0 ]; then oc_dump "$sid"; printf -- "--- to calls ---\n"; cat "$LOGS/to.jsonl" 2>/dev/null; fi' EXIT
    log "OpenCode expands the projected /to command into a coordinator session"
    oc_service_start
    oc_session_create "$sid" homonto || fail "OpenCode did not create the session"
    oc_command "$sid" to "$ARGS" || fail "OpenCode did not run the projected /to command"
    expanded() { oc_messages "$sid" user | jq -e '.data[]? | select(.text | test("three-phase workflow"))' >/dev/null; }
    oc_wait "$sid" 30 expanded || fail "the session prompt is not the projected /to command"
    pass "the session prompt is the expanded .opencode/command/to.md"
    oc_wait "$sid" "$T" || fail "the session did not stop within ${T}s"
    ;;
  *) fail "unknown E2E_TOOL: $TOOL" ;;
esac

scenario "process/$TOOL/create"
to_ran new "$CH" || fail "the session did not create $CH with to new"
[ -f "docs/tasks/$CH/to-state.yaml" ] || fail "no to-state.yaml for $CH"
pass "to new $CH ran from inside the harness"; scenario_pass

scenario "process/$TOOL/advance"
to_ran phase "$CH" || fail "the session did not advance $CH with to phase"
grep -q '^phase: do$' "docs/tasks/$CH/to-state.yaml" || fail "$CH is not at phase do: $(grep phase "docs/tasks/$CH/to-state.yaml")"
pass "to phase $CH: plan → do"; scenario_pass

scenario "process/$TOOL/status"
[ "$(to_phase "$CH")" = "do" ] || fail "to status does not report $CH at do"
snap="$(homonto workflow snapshot --json --config "$PWD/homonto.toml")" || fail "workflow snapshot"
printf '%s' "$snap" | jq -e --arg c "$CH" '[.changes[] | select(.status == "active")] | length == 1 and .[0].name == $c and .[0].phase == "do"' >/dev/null \
  || fail "the workflow snapshot does not show exactly $CH active at do"
pass "to status and the workflow snapshot agree"; scenario_pass

if [ "$TOOL" = claude ]; then
  scenario process/claude/hooks
  # hooks <jq-filter>: this session's hook records matching the filter.
  hooks() { shimlog homonto.jsonl "select(.kind == \"hook\" and .input.session_id == \$sid) | $1" --arg sid "$sid"; }
  [ -n "$(hooks "select(.input.hook_event_name == \"SessionStart\" and .input.agent_type == \"homonto\" and ($HOOK_CONTEXT | test(\"Homonto execution continuity\")))")" ] \
    || fail "the session did not start as the coordinator with the continuation policy"
  TOCMD='(.input.tool_input.command // "" | test("(^|[;&|( ])to "))'
  for verb in new phase; do
    [ -n "$(hooks "select(.input.hook_event_name == \"PreToolUse\" and .input.tool_name == \"Bash\" and .input.agent_type == \"homonto\" and (.input.agent_id // \"\") == \"\" and (.input.tool_input.command | test(\"(^|[;&|( ])to $verb \")) and $HOOK_DECISION == \"allow\")")" ] \
      || fail "the policy hook did not allow the coordinator's to $verb"
  done
  [ -z "$(hooks "select(.input.hook_event_name == \"PreToolUse\" and $TOCMD and $HOOK_DECISION == \"deny\")")" ] \
    || fail "the policy hook denied one of the process's to commands"
  [ -n "$(hooks 'select(.input.hook_event_name == "PostToolUse" and .input.tool_name == "Bash")')" ] \
    || fail "the tool-result hook never ran"
  pass "the coordinator ran the process under homonto's hooks"; scenario_pass
fi

printf '\nSUITE PASS: process/%s\n' "$TOOL"
