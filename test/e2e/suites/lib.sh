# shellcheck shell=bash
# Shared helpers for the real-tool E2E suites. Sourced, not executed.
log()  { printf '\n=== %s ===\n' "$1"; }
pass() { printf '  PASS  %s\n' "$1"; }
fail() {
  printf '\n  FAIL  %s\n' "$1" >&2
  [ -n "${E2E_SCENARIO:-}" ] && printf 'E2E-EVIDENCE\t%s\tfail\t%s\n' "$E2E_SCENARIO" "$1"
  exit 1
}

# Scenario evidence. A suite names each scenario it proves with a stable ID
# (<suite>/<tool>/<scenario>) and ends it with scenario_pass. Each outcome is
# one tab-separated E2E-EVIDENCE line on stdout, which scripts/e2e-matrix.sh
# collects into test/e2e/.out/evidence.tsv. A failure inside a scenario
# records that scenario as failed; a scenario never reached records nothing.
E2E_SCENARIO=""
scenario() { # <id>
  E2E_SCENARIO="$1"
  printf '\n=== scenario %s ===\n' "$1"
}
scenario_pass() {
  [ -n "$E2E_SCENARIO" ] || return 0
  printf 'E2E-EVIDENCE\t%s\tpass\t-\n' "$E2E_SCENARIO"
  E2E_SCENARIO=""
}

# Assert that a captured output string contains a literal needle.
contains() { # <haystack> <needle> <fail-msg>
  case "$1" in *"$2"*) ;; *) fail "$3" ;; esac
}

# Accept Claude Code's workspace trust for a directory, as the user does in
# the trust dialog on first launch. Claude Code loads a project's hooks and
# approves its .mcp.json servers only in a trusted folder; homonto never
# trusts a folder on the user's behalf, so the suites do it here.
claude_trust() { # <dir>
  python3 - "$1" <<'PY'
import json, os, sys
p = os.path.expanduser("~/.claude.json")
c = json.load(open(p)) if os.path.exists(p) else {}
c.setdefault("projects", {}).setdefault(sys.argv[1], {})["hasTrustDialogAccepted"] = True
c["hasCompletedOnboarding"] = True
json.dump(c, open(p, "w"))
PY
}

# Observation shims. They sit ahead of the real binaries on PATH, record each
# call and hand it on unchanged, so a suite can assert on what the harness
# actually ran instead of on the model's prose. Install them before the first
# harness command: OpenCode's background service and Claude Code's tools
# inherit PATH from the process that starts them.
#   $SHIMLOG/homonto.jsonl  every homonto call (Claude hooks with their input
#                           and output; workflow snapshots with their stdout)
#   $SHIMLOG/to.jsonl       every to call with its exit code
# With E2E_HANG_TO_NEW=<change>, the first successful `to new <change>` writes
# $SHIMLOG/boundary (its pid) and then blocks, holding the harness's tool call
# open at a known state until the suite kills the session.
install_shims() { # <log-dir>
  export SHIMLOG="$1"; mkdir -p "$SHIMLOG" /opt/shim
  local real_homonto real_to
  real_homonto="$(command -v homonto)"; real_to="$(command -v to)"
  cat > /opt/shim/homonto <<EOF
#!/usr/bin/env bash
now() { date +%s.%N; }
if [ "\$1 \$2" = "runtime hook" ]; then
  in="\$(cat)"
  out="\$(printf '%s' "\$in" | "$real_homonto" "\$@")"; rc=\$?
  jq -cn --arg t "\$(now)" --argjson input "\$in" --arg out "\$out" --argjson rc "\$rc" \
    '{t:(\$t|tonumber), kind:"hook", input:\$input, out:\$out, rc:\$rc}' >> "$SHIMLOG/homonto.jsonl"
  printf '%s' "\$out"; exit \$rc
fi
if [ "\$1 \$2" = "workflow snapshot" ]; then
  out="\$("$real_homonto" "\$@")"; rc=\$?
  jq -cn --arg t "\$(now)" --arg out "\$out" --argjson rc "\$rc" \
    '{t:(\$t|tonumber), kind:"snapshot", out:\$out, rc:\$rc}' >> "$SHIMLOG/homonto.jsonl"
  printf '%s\n' "\$out"; exit \$rc
fi
exec "$real_homonto" "\$@"
EOF
  cat > /opt/shim/to <<EOF
#!/usr/bin/env bash
"$real_to" "\$@"; rc=\$?
jq -cn --arg t "\$(date +%s.%N)" --arg argv "\$*" --argjson rc "\$rc" \
  '{t:(\$t|tonumber), argv:\$argv, rc:\$rc}' >> "$SHIMLOG/to.jsonl"
if [ "\$rc" = 0 ] && [ -n "\${E2E_HANG_TO_NEW:-}" ] && [ "\$1" = new ] && [ ! -e "$SHIMLOG/boundary" ]; then
  for a in "\$@"; do
    if [ "\$a" = "\$E2E_HANG_TO_NEW" ]; then echo "\$\$" > "$SHIMLOG/boundary"; exec sleep 600; fi
  done
fi
exit \$rc
EOF
  chmod +x /opt/shim/homonto /opt/shim/to
  export PATH="/opt/shim:$PATH"
}

# shimlog <file> <jq-filter> [<jq-args>...]: the matching records, one per line.
shimlog() {
  local file="$SHIMLOG/$1" filter="$2"; shift 2
  [ -f "$file" ] && jq -c "$@" "$filter" "$file" 2>/dev/null
}
# A hook record's injected context and permission decision (jq, for suites).
# shellcheck disable=SC2034
HOOK_CONTEXT='(.out | fromjson? | .hookSpecificOutput.additionalContext // "")'
# shellcheck disable=SC2034
HOOK_DECISION='(.out | fromjson? | .hookSpecificOutput.permissionDecision // "")'

# to_ran <verb> <change> [<since>]: a successful `to <verb> … <change> …`
# reached the binary (at or after <since>, epoch seconds).
to_ran() {
  [ -f "$SHIMLOG/to.jsonl" ] && jq -e --arg v "$1" --arg c "$2" --argjson s "${3:-0}" \
    'select(.rc == 0 and .t >= $s) | .argv | split(" ") | select(.[0] == $v and any(.[]; . == $c))' \
    "$SHIMLOG/to.jsonl" >/dev/null 2>&1
}

# to_phase <change>:the change's phase per `to status --json`, or "".
# (cobra writes the CLIs' output to stderr, so both streams are read.)
to_phase() { to status --json 2>&1 | jq -r --arg n "$1" '.[]? | select(.change == $n) | .phase' 2>/dev/null; }

# A git workspace with homonto.toml for both harnesses on the cells' cheap
# models (Claude Code haiku, a free OpenCode model), the given engines applied
# and initialized, and everything committed so no dirty-work decision is owed.
workspace_init() { # <engine>...
  WORK="$(mktemp -d)"; cd "$WORK" || fail "workspace"
  git init -q . && git config user.email e2e@x && git config user.name e2e
  {
    printf 'harnesses = ["opencode", "claude"]\n'
    for e in "$@"; do printf '\n[engines.%s]\nsource = "builtin:%s"\nscope = "project"\n' "$e" "$e"; done
    printf '\n[routing.default.opencode]\nmodel = "%s"\n' "${E2E_OPENCODE_MODEL:-opencode/deepseek-v4.1-flash-free}"
    printf '\n[routing.default.claude]\nmodel = "haiku"\n'
  } > homonto.toml
  local out
  out="$(homonto apply --yes 2>&1)" || { printf '%s\n' "$out"; fail "homonto apply"; }
  for e in "$@"; do "$e" init >/dev/null 2>&1 || true; done
  git add -A || fail "baseline add"
  git commit -q -m "e2e baseline" || fail "baseline commit"
}

# --- OpenCode 2 server API (opencode api, against the background service) ---
OC_MODEL="${E2E_OPENCODE_MODEL:-opencode/deepseek-v4.1-flash-free}"

# oc_service_start: start the background service (it inherits this shell's
# environment, the shims included) and wait until its API answers.
oc_service_start() {
  opencode service start >/dev/null 2>&1 || true
  local _
  for _ in $(seq 1 30); do
    opencode api session.list >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "the OpenCode background service did not start"
}

# oc_session_create <id> <agent>: a session at the workspace, on the cell's model.
oc_session_create() {
  local provider="${OC_MODEL%%/*}" model="${OC_MODEL#*/}"
  opencode api session.create -d "$(jq -cn --arg id "$1" --arg agent "$2" --arg p "$provider" --arg m "$model" --arg d "$PWD" \
    '{id:$id, agent:$agent, model:{providerID:$p, id:$m}, location:{directory:$d}}')" >/dev/null
}

# oc_command <session> <command> <arguments>: run a projected slash command
# through OpenCode's own command expansion (.opencode/command/<command>.md).
oc_command() {
  opencode api session.command --param sessionID="$1" -d "$(jq -cn --arg n "$2" --arg t "$3" '{name:$n, text:$t}')"
}

# oc_idle <session>: the session ran and has no execution in flight.
oc_idle() {
  opencode api session.get --param sessionID="$1" 2>/dev/null \
    | jq -e '.data.time.busy != null and .data.time.idle != null and .data.time.idle >= .data.time.busy' >/dev/null 2>&1
}

# oc_wait <session> <seconds> [<condition-command>]: wait until the condition
# holds (default: the session is idle). Returns 1 on the deadline.
oc_wait() {
  local sid="$1" deadline=$(( $(date +%s) + $2 )); shift 2
  sleep 3
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if [ $# -gt 0 ]; then "$@" && return 0; else oc_idle "$sid" && return 0; fi
    sleep 3
  done
  return 1
}

# oc_messages <session> [<type>]: the session's messages, oldest first.
oc_messages() {
  opencode api session.message.list --param sessionID="$1" --param order=asc --param limit=200 ${2:+--param type="$2"} 2>/dev/null
}

# oc_dump <session>: a compact transcript, for failure diagnosis.
oc_dump() {
  printf '\n--- session %s ---\n' "$1"
  oc_messages "$1" | jq -c '.data[]? | {type, agent, text: ((.text // .summary // "")[0:160]),
    tools: [(.content // [])[] | select(.type == "tool") | {name, status: .state.status}]}' 2>/dev/null | tail -40
  opencode api session.permission.list --param sessionID="$1" 2>/dev/null | head -c 600; echo
}
