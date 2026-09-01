#!/usr/bin/env bash
# Suite: analyze — not a pass/fail test but a structured dump of the container's
# internal state after a representative homonto apply (OpenCode — the only
# adapter since v0.13.0 — plus the onto framework). The orchestrator captures
# this to a file so you can inspect exactly what homonto wrote inside the
# image. Contains no secrets (state stores hashes).
set -uo pipefail
source /opt/e2e-suites/lib.sh

WORK="$(mktemp -d)"; cd "$WORK"
git init -q; git config user.email e2e@example.com; git config user.name e2e
mkdir -p homonto/skills/e2e-demo
printf -- '---\nname: e2e-demo\ndescription: d\n---\nbody\n' > homonto/skills/e2e-demo/SKILL.md
cat > homonto.toml <<'EOF'
[frameworks.onto]
source = "builtin:onto"
scope = "project"

# Per-agent models for the framework's expanded subagents (no tiers).
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"

[mcps.e2e-probe]
command = ["codegraph", "serve", "--mcp"]

[skills.e2e-demo]
source = "local:e2e-demo"
scope = "user"

[settings.opencode]
theme = "opencode-dark"
EOF
homonto apply --yes >/dev/null 2>&1 || homonto apply --yes

echo "##################### CONTAINER INTERNALS ANALYSIS #####################"
echo
echo "## 1. installed binaries"
# homonto/onto route cobra output to stderr, so capture 2>&1.
printf '   homonto   %s\n' "$(homonto version 2>&1)"
printf '   onto      %s\n' "$(onto version 2>&1)"
printf '   opencode  %s\n' "$(opencode --version 2>/dev/null)"

echo
echo "## 2. homonto-managed tool config files (what apply wrote)"
f="$HOME/.config/opencode/opencode.jsonc"
if [ -f "$f" ]; then echo "   --- $f ---"; sed -e 's/^/       /' "$f" | head -30; fi

echo
echo "## 3. projected skill symlinks (owned content, linked not copied)"
find "$HOME/.config/opencode/skills" -maxdepth 1 -type l \
  -printf '   %p -> %l\n' 2>/dev/null || true

echo
echo "## 4. materialized builtin catalog (.homonto/catalog)"
find "$WORK/.homonto/catalog" -maxdepth 2 -mindepth 1 -type d -printf '   %P\n' 2>/dev/null | sort | head -40

echo
echo "## 5. homonto state (unresolved refs + hashes, never plaintext secrets)"
sed -e 's/^/   /' "$WORK/.homonto/state.json" 2>/dev/null | head -40

echo
echo "## 6. the real tool sees homonto's projection"
echo "   -- opencode mcp list --"
opencode mcp list 2>&1 | sed -e 's/^/   /' | head -10

echo
echo "##################### END ANALYSIS #####################"
