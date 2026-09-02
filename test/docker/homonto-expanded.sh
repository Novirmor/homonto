#!/bin/sh
# Suite: homonto-expanded — builtin framework materialization, skill/command/
# subagent links into the tool dir, OpenCode subagent renders, plugin
# projection, and OpenCode TUI projection, all against a disposable $HOME.
set -eu
SUITE=homonto-expanded
. "$(dirname "$0")/lib.sh"

HOME="$(mktemp -d)"; export HOME
W="$(mktemp -d)"; cd "$W"

cat > homonto.toml <<'EOF'
[frameworks.onto]
source = "builtin:onto"
scope = "project"

# Per-agent models (no tiers): every framework-expanded agent declares a model
# in its [subagents.<n>.opencode] block — a block with no source tunes the
# framework's agent rather than re-declaring it. OpenCode spells a variant as
# its own field and has no effort concept, so a block carries model/variant
# only. The dispatcher `onto` declares one too; it renders for OpenCode like
# every other agent (mode: primary).
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
variant = "1m"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
# The skeptic runs the reviewer's model and variant.
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
variant = "1m"

# onto ships all builtin subagents as framework subagents, so declaring one
# explicitly would collide. Use a local: agent file (homonto/subagents/) as the
# standalone explicit subagent that the prune test later removes.
[subagents.nav-agent]
source = "local:nav-agent"
scope = "project"
targets = ["opencode"]

[plugins.opencode.hud]
source = "@e2e/hud"

[tui.opencode]
theme = "gruvbox"
EOF

mkdir -p homonto/subagents
cat > homonto/subagents/nav-agent.md <<'AGENT'
---
description: local standalone test agent
---
Route the user to the right change.
AGENT

log "apply projects the expanded surface"
"$HOMONTO" apply --yes

log "builtin catalog materialization"
is_dir  "$W/.homonto/catalog/skills/onto"
is_file "$W/.homonto/catalog/commands/onto.md"
# onto ships four specialist subagents; nav-agent is the explicit local one
# (local sources link straight from homonto/subagents/, no materialization).
is_file "$W/.homonto/catalog/subagents/onto-reviewer.md"
is_file "$W/.homonto/catalog/subagents/onto-explorer.md"
is_file "$W/.homonto/catalog/subagents/onto-implementer.md"
is_file "$W/.homonto/catalog/subagents/onto-skeptic.md"
# Homonto-block subagents materialize an OpenCode variant: the render turns the
# neutral block into OpenCode's native `permission:` map. A read-only spawn:[]
# agent (the reviewer) denies exactly edit and task — question is denied too
# because its block sets dialogs: false — while bash stays at the tool default,
# and the declared model and variant stamp as their own frontmatter fields.
RVAR="$W/.homonto/catalog/subagents/onto-reviewer.opencode.md"
in_file "$RVAR" '  edit: deny'
in_file "$RVAR" '  task: deny'
if grep -q 'bash: deny' "$RVAR"; then fail "reviewer keeps bash; only the block's denials may render"; fi
in_file "$RVAR" 'model: anthropic/claude-opus-4-8'
in_file "$RVAR" 'variant: 1m'
# The implementer edits (coding model) but still spawns nothing: the only task
# denial is spawning — edit stays available (absent from the permission map).
IVAR="$W/.homonto/catalog/subagents/onto-implementer.opencode.md"
in_file "$IVAR" 'model: anthropic/claude-sonnet-4'
in_file "$IVAR" '  task: deny'
if grep -q 'edit: deny' "$IVAR"; then fail "edit-capable implementer must not deny edit"; fi
# The onto primary agent renders for OpenCode like any other agent: mode is
# re-emitted from primary, its iteration budget renders as steps:, and its
# delegation topology renders as task allows over a deny-all default.
PVAR="$W/.homonto/catalog/subagents/onto.opencode.md"
in_file "$PVAR" 'mode: primary'
in_file "$PVAR" 'steps: 1200'
in_file "$PVAR" '"onto-reviewer": allow'
ok "framework skills, commands, and subagents materialized (opencode render invariants hold)"

# Assert each tool entry is a symlink AND that it actually resolves to real
# catalog content — a relative target computed against the wrong base dangles,
# and a dangling skill/command link is invisible to the tool (e.g. OpenCode's
# skill discovery skips it). is_dir/is_file follow the link, so they fail on a
# dangling target; link_to only string-matched and missed exactly that bug.
log "tool links point at (and resolve to) the materialized catalog"
is_link "$W/.opencode/skills/onto";                 is_dir  "$W/.opencode/skills/onto"
is_link "$W/.opencode/agent/onto-reviewer.md";     is_file "$W/.opencode/agent/onto-reviewer.md"
is_link "$W/.opencode/agent/onto-explorer.md"; is_file "$W/.opencode/agent/onto-explorer.md"
is_link "$W/.opencode/agent/onto-implementer.md";  is_file "$W/.opencode/agent/onto-implementer.md"
is_link "$W/.opencode/agent/nav-agent.md";         is_file "$W/.opencode/agent/nav-agent.md"
# The onto primary agent projects for OpenCode like any other agent (its entry
# point is still the /onto command → onto skill, but the agent renders too).
is_link "$W/.opencode/agent/onto.md"; is_file "$W/.opencode/agent/onto.md"
# The onto framework ships a command per phase/preset — the dispatcher plus every
# onto-* skill — so each phase is directly invocable. Assert the whole set links
# and resolves, not just the dispatcher.
for c in onto onto-open onto-design onto-build onto-verify onto-close onto-fix onto-tweak onto-no-slop; do
	is_link "$W/.opencode/command/$c.md"; is_file "$W/.opencode/command/$c.md"
done
ok "skill, full command set, and subagent links resolve to the catalog"

log "plugin projected into opencode's plugin array"
OJSONC="$HOME/.config/opencode/opencode.jsonc"
in_file "$OJSONC" '"plugin"'
in_file "$OJSONC" '@e2e/hud'
ok "plugin array present with the declared plugin"

log "opencode TUI projected into tui.json"
in_file "$HOME/.config/opencode/tui.json" 'gruvbox'
ok "tui.json theme projected"

log "re-apply is idempotent"
out="$("$HOMONTO" apply --yes 2>&1)"; printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second apply was not idempotent"
ok "idempotent re-apply"

log "prune on removal: drop the explicit subagent, re-apply removes its link"
sed '/\[subagents.nav-agent\]/,/targets = \["opencode"\]/d' homonto.toml > homonto.toml.new
mv homonto.toml.new homonto.toml
"$HOMONTO" apply --yes >/dev/null 2>&1
absent "$W/.opencode/agent/nav-agent.md"
# The framework-provided subagents are NOT de-declared, so they must survive.
is_file "$W/.opencode/agent/onto-reviewer.md"
is_file "$W/.opencode/agent/onto-explorer.md"
is_file "$W/.opencode/agent/onto-implementer.md"
ok "de-declared subagent link pruned; framework subagents retained"

printf '\nSUITE PASS: %s\n' "$SUITE"
