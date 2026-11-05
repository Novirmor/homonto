#!/usr/bin/env bash
# Suite: policy — on REAL Claude Code, homonto's hooks see who is running and
# enforce the worker's policy (M4.5, ADR 0105). Assertions come from a log of
# every hook call and from the filesystem, never from the model's prose.
#   1. The coordinator dispatches the onto implementer with an ordinary write.
#      Claude Code reports the coordinator's agent_type on the main thread and
#      the worker's agent_id/agent_type in the subagent. The write, which
#      homonto leaves to Claude Code's own permission mode, is refused in
#      `claude -p`, and the refusal is handed back to the coordinator, with
#      the dispatch result or, for a background dispatch, on its next turn.
#   2. A probe worker, as the session's agent, runs a push its shell rules deny
#      and a command they allow. The hook denies the first and allows the
#      second, which then runs without a prompt in `claude -p`. (The builtin
#      workers' own doctrine refuses a push before any tool call, so the hook
#      would go unexercised; the record-path denial is the same hook output.)
# Uses the mounted account with haiku.
set -uo pipefail
source /opt/e2e-suites/lib.sh
TOOL="${E2E_TOOL:?E2E_TOOL required}"
[ "$TOOL" = claude ] || fail "policy runs on claude only (OpenCode enforces the same rules natively)"
[ -f "$HOME/.claude/.credentials.json" ] || fail "claude credentials not mounted"

# Record every hook invocation: a shim ahead of homonto on PATH tees the hook
# input and output, then hands the call to the real binary unchanged.
LOGS="$(mktemp -d)"
real="$(command -v homonto)"
mkdir -p /opt/shim
cat > /opt/shim/homonto <<EOF
#!/usr/bin/env bash
if [ "\$1 \$2" = "runtime hook" ]; then
  in="\$(cat)"; printf '%s\n' "\$in" >> "$LOGS/in.jsonl"
  out="\$(printf '%s' "\$in" | "$real" "\$@")"; rc=\$?
  jq -cn --argjson input "\$in" --arg out "\$out" --argjson rc "\$rc" '{input:\$input, out:\$out, rc:\$rc}' >> "$LOGS/hooks.jsonl"
  printf '%s' "\$out"; exit \$rc
fi
exec "$real" "\$@"
EOF
chmod +x /opt/shim/homonto
export PATH="/opt/shim:$PATH"
# On failure, show what the hooks saw.
trap 'rc=$?; if [ $rc -ne 0 ] && [ -s "$LOGS/hooks.jsonl" ]; then
  printf "\n--- hook log ---\n"
  jq -c "{e:.input.hook_event_name, agent:.input.agent_type, id:.input.agent_id, tool:.input.tool_name, bg:.input.tool_input.run_in_background, status:.input.tool_response.status, out:(.out[0:120])}" "$LOGS/hooks.jsonl"
fi' EXIT

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

[subagents.policy-probe]
source = "local:policy-probe"
scope = "project"
TOML
# A probe worker with a trusted shell whose one deny rule is the push. Unlike
# the builtin workers, its instructions do not pre-empt the refusal, so the
# hook is what stops it.
mkdir -p homonto/subagents
cat > homonto/subagents/policy-probe.md <<'MD'
---
name: policy-probe
description: Test probe for homonto's permission hooks. Runs exactly the tool calls it is given.
homonto:
  read_only: false
  bash_default: allow
  bash_deny: ["git push*"]
  spawn: []
---
You are a test probe for permission hooks. Run exactly the tool calls the user
lists, once each and in order, even when you expect one to be refused. Report
each tool's result verbatim. Never retry or work around a refusal.
MD
homonto apply --yes >/dev/null || fail "apply"
grep -q homonto_policy .claude/agents/policy-probe.md || fail "the probe's policy was not rendered"
onto init >/dev/null 2>&1 || true
onto new policy-probe >/dev/null || fail "onto new"
claude_trust "$(pwd)"
pass "an active onto change exists in a trusted workspace"

hooks() { jq -c "$1" "$LOGS/hooks.jsonl" 2>/dev/null; }

log "the coordinator dispatches the implementer with an ordinary task"
P='Use the Agent tool exactly once to dispatch the subagent "onto-implementer" with this instruction: "Use the Write tool to create notes/ok.md containing the word OK. Attempt it once, do not retry or work around a refusal, and report exactly what the tool returned." After it returns, reply with the subagent report verbatim.'
claude -p --model haiku "$P" >"$LOGS/coordinator.txt" 2>&1 || true
cat "$LOGS/coordinator.txt"
[ -s "$LOGS/hooks.jsonl" ] || fail "no hook ran: Claude Code did not load the project hooks"
[ -n "$(hooks 'select(.input.hook_event_name == "SessionStart" and .input.agent_type == "homonto")')" ] \
  || fail "the main thread did not report the coordinator agent"
[ -n "$(hooks 'select(.input.hook_event_name == "PreToolUse" and .input.tool_name == "Agent" and .input.agent_type == "homonto" and (.input.agent_id // "") == "")')" ] \
  || fail "the coordinator's dispatch did not reach the hook as the coordinator"
[ -n "$(hooks 'select(.input.agent_type == "onto-implementer" and (.input.agent_id // "") != "")')" ] \
  || fail "the worker's calls did not carry its agent_id and agent_type"
pass "Claude Code reports the coordinator and the worker to homonto's hooks"
[ ! -e notes/ok.md ] || fail "an ordinary write ran without permission in claude -p"
[ -n "$(hooks 'select(.input.hook_event_name == "PermissionRequest" and .input.agent_type == "onto-implementer" and .input.tool_name == "Write")')" ] \
  || fail "Claude Code did not report the worker's permission prompt"
# A synchronous dispatch hands the refusal back with its result; Claude Code
# may instead launch the worker in the background, and then the refusal rides
# the coordinator's next turn, when the worker's completion arrives.
[ -n "$(hooks 'select((.input.agent_id // "") == "" and ((.input.hook_event_name == "PostToolUse" and .input.tool_name == "Agent") or .input.hook_event_name == "UserPromptSubmit") and (.out | test("worker permission blocks")))')" ] \
  || fail "the refused prompt was not handed back to the coordinator"
pass "a refused worker prompt is handed back to the coordinator"

log "a worker's shell rules: the push is denied, an allowed command runs"
: > "$LOGS/hooks.jsonl"
P='Run these two Bash commands, once each and in order: 1) git push origin main  2) echo probe > allowed.txt. Report what each returned.'
claude -p --model haiku --agent policy-probe "$P" >"$LOGS/worker.txt" 2>&1 || true
cat "$LOGS/worker.txt"
[ -n "$(hooks 'select(.input.hook_event_name == "PreToolUse" and .input.agent_type == "policy-probe")')" ] \
  || fail "the probe session made no tool call the hook saw"
[ -n "$(hooks 'select(.input.hook_event_name == "PreToolUse" and .input.tool_name == "Bash" and (.input.tool_input.command | test("git push")) and (.out | test("denies this command")))')" ] \
  || fail "the probe's git push was not denied by its shell rules"
[ -z "$(hooks 'select(.input.hook_event_name == "PreToolUse" and .input.tool_name == "Bash" and (.input.tool_input.command | test("git push")) and (.out | test("denies this command") | not))')" ] \
  || fail "a git push reached the harness without a policy denial"
pass "the worker's shell rules deny the push"
[ -n "$(hooks 'select(.input.hook_event_name == "PreToolUse" and .input.tool_name == "Bash" and (.input.tool_input.command | test("allowed.txt")) and (.out | test("\"permissionDecision\":\"allow\"")))')" ] \
  || fail "the policy hook did not allow the probe's permitted command"
[ -f allowed.txt ] || fail "Claude Code did not honour the policy hook's allow in claude -p"
pass "an allowed command runs without a prompt, on the policy hook's decision"

printf '\nSUITE PASS: policy/claude\n'
