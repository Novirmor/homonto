#!/usr/bin/env bash
# Suite: reference — the PUBLISHED reference extension set
# (Novirmor/homonto-reference-set, pinned in examples/reference-set/published.toml
# and copied to /opt/reference/published.toml), installed over https as
# digest-pinned remote: sources, projected onto OpenCode and Claude Code, and run
# through its engine's native lifecycle with the real CLI: start, bind, task
# create, resume (inspect), integration check, snapshot, finish, release. The
# engine and tracker binaries run from the verified, content-addressed copy
# homonto materialized. No homonto source names these packages (M4.8). No
# account needed; the container fetches the release assets from GitHub.
set -uo pipefail
source /opt/e2e-suites/lib.sh

WORK="$(mktemp -d)"; cd "$WORK" || exit 1
mkdir -p api tickets
git -C api init -q -b main
git -C api -c user.email=e2e@x -c user.name=e2e commit -q --allow-empty -m base
BASE="$(git -C api rev-parse HEAD)"

# The published release's pins: one url and content digest per package.
PUB=/opt/reference/published.toml
[ -f "$PUB" ] || fail "$PUB missing from the image"
toml_str() { sed -n "s/^$1 = \"\(.*\)\"$/\1/p" "$PUB" | head -1; }
declare -A url pin
pkg_field() { # <package> <field>
  sed -n "/^\[packages\.$1\]$/,/^\[/s/^$2 = \"\(.*\)\"$/\1/p" "$PUB" | head -1
}
for p in ticket-engine ticket-workflow ticket-service; do
  url[$p]="$(pkg_field "$p" url)"; pin[$p]="$(pkg_field "$p" digest)"
  { [ -n "${url[$p]}" ] && [ -n "${pin[$p]}" ]; } || fail "no url/digest for $p in $PUB"
done
ext_repo="$(toml_str repository)"; ext_tag="$(toml_str tag)"; ext_commit="$(toml_str commit)"

write_config() {
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
source = "remote:${url[ticket-engine]}"
digest = "${pin[ticket-engine]}"
scope = "project"

[workflows.ticket-workflow]
source = "remote:${url[ticket-workflow]}"
digest = "${pin[ticket-workflow]}"
scope = "project"

[services.ticket-service]
source = "remote:${url[ticket-service]}"
digest = "${pin[ticket-service]}"
scope = "project"

[workflows.ticket-workflow.processes.bug]
steps = ["reproduce", "fix", "review"]

[routing.default.opencode]
model = "opencode/deepseek-v4.1-flash-free"

[routing.default.claude]
model = "haiku"
TOML
}

write_config
log "homonto apply ($ext_tag over https, digest-pinned) → opencode + claude"
homonto apply --yes || fail "apply of the pinned published reference set failed"
pass "the three published packages fetched, verified against their pins and applied"
# The binaries come from the verified, content-addressed copies homonto just
# materialized (.homonto/cache/remote/<algo>/<hex>), not from any other tree.
cache_dir() { printf '%s/.homonto/cache/remote/%s' "$(pwd)" "$(printf '%s' "$1" | tr : /)"; }
for p in ticket-engine ticket-service; do
  d="$(cache_dir "${pin[$p]}")/bin"
  [ -d "$d" ] || fail "$p not materialized at $d"
  PATH="$d:$PATH"
done
export PATH
[ "$(command -v ticket-engine)" = "$(cache_dir "${pin[ticket-engine]}")/bin/ticket-engine" ] || fail "ticket-engine does not resolve to the pinned copy"
pass "engine and tracker binaries run from the pinned copies"
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
resumed_phase="$(homonto workflow snapshot --json | jq -er '.changes[] | select(.workflow == "ticket-engine" and .name == "abc-12" and .phase == "implement") | .phase')" \
  || fail "the workflow snapshot lacks the engine's change"
pass "the snapshot every harness injects names the engine's change"
log "finish and release"
ticket-engine finish abc-12 --root "$ROOT" || fail "ticket-engine finish"
homonto worktree remove abc-12 --workflow ticket-engine --repo api --yes || fail "worktree remove"
homonto workspace task cleanup abc-12 --yes || fail "task cleanup"
pass "released the binding and the task"

# One line of matrix evidence naming exactly what this run exercised.
printf 'EVIDENCE reference: extension=%s@%s commit=%s ticket-engine=%s ticket-workflow=%s ticket-service=%s homonto=%s opencode=%s claude=%s resumed=%s phase=%s\n' \
  "$ext_repo" "$ext_tag" "$ext_commit" "${pin[ticket-engine]}" "${pin[ticket-workflow]}" "${pin[ticket-service]}" \
  "$(homonto version 2>&1 | head -1 | tr ' ' '_')" "$(opencode --version 2>&1 | head -1 | tr ' ' '_')" "$(claude --version 2>&1 | head -1 | tr ' ' '_')" \
  "$(jq -r .native_id /tmp/inspect.json)" "$resumed_phase"

printf '\nSUITE PASS: reference\n'
