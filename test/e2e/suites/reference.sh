#!/usr/bin/env bash
# Suite: reference — the reference extension set (examples/reference-set),
# installed as digest-pinned remote: archives, projected onto OpenCode and
# Claude Code, and run through its engine's native lifecycle with the real
# CLI: start, bind, task create, resume (inspect), integration check,
# snapshot, finish, release. No homonto source names these packages (M4.8).
# No account needed.
set -uo pipefail
source /opt/e2e-suites/lib.sh

WORK="$(mktemp -d)"; cd "$WORK"
mkdir -p api tickets
git -C api init -q -b main
git -C api -c user.email=e2e@x -c user.name=e2e commit -q --allow-empty -m base
BASE="$(git -C api rev-parse HEAD)"

write_config() { # <engine-digest> <workflow-digest> <service-digest>
  cat > homonto.toml <<TOML
schema_version = 2
harnesses = ["opencode", "claude"]

[repos]
api = "./api"

[workflow]
root = "records"

[worktrees]
dir = "./worktrees"

[integration.unit]
repos = ["api"]
repo = "api"
cwd = "."
command = ["git", "status", "--short"]

[engines.ticket-engine]
source = "remote:file:///opt/reference/ticket-engine.tar.gz"
digest = "$1"
scope = "project"

[workflows.ticket-workflow]
source = "remote:file:///opt/reference/ticket-workflow.tar.gz"
digest = "$2"
scope = "project"

[services.ticket-service]
source = "remote:file:///opt/reference/ticket-service.tar.gz"
digest = "$3"
scope = "project"

[workflows.ticket-workflow.processes.bug]
steps = ["reproduce", "fix", "review"]

[routing.default.opencode]
model = "opencode/deepseek-v4.1-flash-free"

[routing.default.claude]
model = "haiku"
TOML
}

# Trust on first use, as remote-sources.md describes: a wrong pin names the
# content's digest, which is then pinned.
zero="sha256:$(printf '0%.0s' $(seq 64))"
declare -A pin=([ticket-engine]="$zero" [ticket-workflow]="$zero" [ticket-service]="$zero")
for _ in 1 2 3; do
  write_config "${pin[ticket-engine]}" "${pin[ticket-workflow]}" "${pin[ticket-service]}"
  out="$(homonto plan 2>&1 || true)"
  line="$(printf '%s' "$out" | grep -o 'reference/[a-z-]*\.tar\.gz": declared sha256:[0-9a-f]* but content is sha256:[0-9a-f]\{64\}' | head -1)"
  [ -n "$line" ] || break
  name="$(printf '%s' "$line" | sed 's|reference/\([a-z-]*\)\.tar\.gz.*|\1|')"
  pin[$name]="$(printf '%s' "$line" | sed 's/.*content is //')"
done
write_config "${pin[ticket-engine]}" "${pin[ticket-workflow]}" "${pin[ticket-service]}"
log "homonto apply (remote, digest-pinned) → opencode + claude"
homonto apply --yes || fail "apply of the pinned reference set failed"
pass "the three remote packages verified and applied"
for f in .opencode/skills/ticket-to-delivery/SKILL.md .claude/skills/ticket-to-delivery/SKILL.md \
         .opencode/command/ticket.md .claude/commands/ticket.md \
         .opencode/agent/ticket-reviewer.md .claude/agents/ticket-reviewer.md .claude/agents/ticket-implementer.md; do
  [ -e "$f" ] || fail "$f not projected"
done
grep -q '"ticket-cli comment\*": ask' .opencode/agent/homonto.md || fail "service asks missing from the OpenCode coordinator"
grep -q 'homonto_policy' .claude/agents/ticket-implementer.md || fail "Claude implementer policy missing"
pass "workflow, engine and service projected natively on both harnesses"

ROOT="$(pwd)/records"
log "the engine's own binary starts the change"
id="$(ticket-engine start abc-12 --root "$ROOT" --repo api --base "$BASE" --branch main)" || fail "ticket-engine start"
ticket-engine phase abc-12 --root "$ROOT" implement || fail "ticket-engine phase"
homonto worktree create abc-12 --workflow ticket-engine --repo api --base main --branch ticket/abc-12 --json \
  || fail "homonto could not bind a checkout to the external engine's change"
pass "homonto bound the change's checkout through its registry"
homonto workspace task create abc-12 --engine ticket-engine --change abc-12 --repo api --isolation existing >/tmp/task.json \
  || { cat /tmp/task.json; fail "task create"; }
jq -e --arg id "id:$id" '.native_id == $id' /tmp/task.json >/dev/null || fail "task did not record the engine's generation"
pass "workspace task created against the engine's lifecycle record"
log "resume: a later invocation reloads the same task"
homonto workspace task inspect abc-12 >/tmp/inspect.json || fail "task inspect"
jq -e --arg id "id:$id" '.native_id == $id' /tmp/inspect.json >/dev/null || fail "resume lost the generation"
homonto workspace task check abc-12 --writers-paused >/tmp/check.json || { cat /tmp/check.json; fail "integration check"; }
pass "resume and integration check"
homonto workflow snapshot --json | jq -e '.changes[] | select(.workflow == "ticket-engine" and .name == "abc-12" and .phase == "implement")' >/dev/null \
  || fail "the workflow snapshot lacks the engine's change"
pass "the snapshot every harness injects names the engine's change"
log "finish and release"
ticket-engine finish abc-12 --root "$ROOT" || fail "ticket-engine finish"
homonto worktree remove abc-12 --workflow ticket-engine --repo api --yes || fail "worktree remove"
homonto workspace task cleanup abc-12 --yes || fail "task cleanup"
pass "released the binding and the task"

printf '\nSUITE PASS: reference\n'
