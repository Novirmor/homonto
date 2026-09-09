#!/bin/sh
# Suite: h-surface — the h GitHub-intake framework applied alone: transitive
# onto/to installation, command and skill projection, rendered worker
# permissions (edit/bash/task denied, web allowed), trusted workspace execution,
# and both binaries' gates accepting an applied [frameworks.h].
set -eu
SUITE=h-surface
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"
git init -q
git config user.email e2e@example.com
git config user.name e2e

cat > homonto.toml <<'EOF'
# h alone: it depends on onto and to in the catalog, so applying it must
# transitively install both frameworks and every expanded agent needs an
# explicit model block (ADR 0045: one shared homonto primary, no onto/to
# primary blocks exist anymore).
[frameworks.h]
source = "builtin:h"
scope = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.h-spike.opencode]
model = "openai/gpt-5-mini"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
EOF

log "homonto apply installs h plus both transitive frameworks"
"$HOMONTO" apply --yes >/dev/null
is_dir "$W/.homonto/catalog/skills/onto"
is_dir "$W/.homonto/catalog/skills/to"
for c in h-spike-issue h-resolve-issue h-review-pr h-continue-pr h-review-batch; do
	is_link "$W/.opencode/command/$c.md"; is_file "$W/.opencode/command/$c.md"
done
ok "h commands project; onto and to materialize transitively"

log "read-only workers allow supporting web research without write or shell access"
for worker in h-spike h-review onto-explorer onto-reviewer onto-skeptic to-explorer to-reviewer to-skeptic; do
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
# Trusted shell has finite publication/destruction exceptions, not composition
# guards. in_file regex-matches (BRE): escape the literal asterisks.
in_file "$PVAR" '"git push \*": ask'
in_file "$PVAR" '"gh pr comment\*": ask'
in_file "$PVAR" '"onto bypass\*": deny'
in_file "$PVAR" '"to bypass\*": deny'
is_link "$W/.opencode/agent/homonto.md"; is_file "$W/.opencode/agent/homonto.md"
for agent in homonto onto-implementer to-implementer; do
	RVAR="$W/.homonto/catalog/subagents/$agent.opencode.md"
	in_file "$RVAR" '  webfetch: allow'
	in_file "$RVAR" '  websearch: allow'
	in_file "$RVAR" '"\*": allow'
	in_file "$RVAR" '"rm -r\*": ask'
	in_file "$RVAR" '"rtk proxy rm -r\*": ask'
	if grep -q '"\*;\*": ask' "$RVAR"; then fail "trusted shell must not retain composition guards"; fi
	if [ "$agent" != homonto ]; then
		in_file "$RVAR" '"onto \*": deny'
		in_file "$RVAR" '"to \*": deny'
		in_file "$RVAR" '"homonto \*": deny'
		in_file "$RVAR" '"gh pr comment\*": deny'
		in_file "$RVAR" '"gh api\*": deny'
		in_file "$RVAR" '"git push \*": deny'
		in_file "$RVAR" '"rtk proxy git push \*": deny'
		in_file "$RVAR" '  task: deny'
		in_file "$RVAR" '  question: deny'
	fi
done
ok "trusted general shell allowed; worker write and publishing boundaries retained"

log "an applied h satisfies both workflow gates"
"$ONTO" init >/dev/null
is_dir "$W/docs/changes"
"$TO" init >/dev/null
is_dir "$W/docs/tasks"
ok "onto init and to init accept [frameworks.h]"

log "re-apply is idempotent"
out="$("$HOMONTO" apply --yes 2>&1)"; printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second apply was not idempotent"
ok "idempotent re-apply"

printf '\nSUITE PASS: %s\n' "$SUITE"
