#!/bin/sh
# Suite: h-surface — the standalone h GitHub workflow: its command and worker
# projection, rendered worker permissions, and rejection by engine binary gates.
set -eu
SUITE=h-surface
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"
git init -q
git config user.email e2e@example.com
git config user.name e2e

cat > homonto.toml <<'EOF'
# h alone installs only its own shared coordinator and read-only workers.
[workflows.h]
source = "builtin:h"
scope = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.h-spike.opencode]
model = "openai/gpt-5-mini"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-4-8"
EOF

log "homonto apply installs only standalone h resources"
"$HOMONTO" apply --yes >/dev/null
test ! -e "$W/.homonto/catalog/skills/onto" || fail "h must not install onto"
test ! -e "$W/.homonto/catalog/skills/to" || fail "h must not install to"
for c in h-spike-issue h-resolve-issue h-review-pr h-continue-pr h-review-batch; do
	is_link "$W/.opencode/command/$c.md"; is_file "$W/.opencode/command/$c.md"
done
ok "h commands project without onto or to"

log "read-only workers allow supporting web research without write or shell access"
for worker in h-spike h-review; do
	RVAR="$W/.homonto/catalog/subagents/$worker.opencode.md"
	in_file "$RVAR" '  edit: deny'
	in_file "$RVAR" '  bash: deny'
	in_file "$RVAR" '  webfetch: allow'
	in_file "$RVAR" '  websearch: allow'
	in_file "$RVAR" '  task: deny'
	in_file "$RVAR" '  question: deny'
	is_link "$W/.opencode/agent/$worker.md"; is_file "$W/.opencode/agent/$worker.md"
done
# The coordinator owns GitHub intake/publication; supporting research is allowed.
PVAR="$W/.homonto/catalog/subagents/homonto.opencode.md"
in_file "$PVAR" 'mode: primary'
in_file "$PVAR" '  webfetch: allow'
in_file "$PVAR" '  websearch: allow'
# ADR 0070 trusts ordinary coordinator delivery; protected operations still ask.
# in_file regex-matches (BRE): escape the literal asterisks.
in_file "$PVAR" '"git push --force\*": ask'
in_file "$PVAR" '"git push --delete\*": ask'
in_file "$PVAR" '"gh pr merge\*": ask'
in_file "$PVAR" '"onto bypass\*": ask'
in_file "$PVAR" '"to bypass\*": ask'
for prefix in '' 'rtk ' 'rtk proxy '; do
	for pattern in 'git push *' 'gh pr comment*' 'gh pr create*' 'gh api*' 'gh run rerun*'; do
		for action in ask deny; do
			if grep -Fq "\"${prefix}${pattern}\": $action" "$PVAR"; then
				fail "coordinator routine delivery must inherit allow: ${prefix}${pattern}"
			fi
		done
	done
done
is_link "$W/.opencode/agent/homonto.md"; is_file "$W/.opencode/agent/homonto.md"
for agent in homonto; do
	RVAR="$W/.homonto/catalog/subagents/$agent.opencode.md"
	in_file "$RVAR" '  webfetch: allow'
	in_file "$RVAR" '  websearch: allow'
	in_file "$RVAR" '"\*": allow'
	in_file "$RVAR" '"rm -r\*": ask'
	in_file "$RVAR" '"rtk proxy rm -r\*": ask'
	if grep -q '"\*;\*": ask' "$RVAR"; then fail "trusted shell must not retain composition guards"; fi
done
ok "trusted general shell allowed; worker write and publishing boundaries retained"

log "an applied h does not satisfy engine binary gates"
if "$ONTO" init >/dev/null 2>&1; then fail "onto accepted h without [engines.onto]"; fi
if "$TO" init >/dev/null 2>&1; then fail "to accepted h without [engines.to]"; fi
ok "onto and to require their own engine declarations"

log "re-apply is idempotent"
out="$("$HOMONTO" apply --yes 2>&1)"; printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second apply was not idempotent"
ok "idempotent re-apply"

printf '\nSUITE PASS: %s\n' "$SUITE"
