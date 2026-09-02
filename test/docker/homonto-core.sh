#!/bin/sh
# End-to-end smoke test for the compiled homonto binary. Runs a real
# `apply` against a throwaway $HOME and workspace, so it exercises the actual
# os.UserHomeDir() code path (not the test-only HOME override) without touching
# any real system. Meant to run inside the Docker image (test/docker/Dockerfile).
set -eu

fail() { printf '\nSMOKE FAIL: %s\n' "$1" >&2; exit 1; }
log()  { printf '\n=== %s ===\n' "$1"; }

# Disposable HOME and workspace — everything homonto writes lands here.
HOME="$(mktemp -d)"
export HOME
WORK="$(mktemp -d)"
cd "$WORK"

# A minimal owned skill for homonto to link.
mkdir -p homonto/skills/demo
cat > homonto/skills/demo/SKILL.md <<'EOF'
---
name: demo
description: smoke-test skill
---
demo skill body
EOF

OPEN_USER="$HOME/.config/opencode/skills/demo"
OPEN_PROJ="$WORK/.opencode/skills/demo"
SRC="$WORK/homonto/skills/demo"

# ---------------------------------------------------------------- user scope
log "user scope: apply"
printf '[skills.demo]\nsource = "local:demo"\nscope = "user"\n' > homonto.toml
homonto apply --yes

[ -L "$OPEN_USER" ]   || fail "opencode user link not created"
[ "$(readlink "$OPEN_USER")" = "$SRC" ] || fail "user link points at $(readlink "$OPEN_USER"), want $SRC"
[ -e "$OPEN_PROJ" ] && fail "user scope must not create a project link"

log "user scope: second apply is idempotent"
out="$(homonto apply --yes 2>&1)"
printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second user apply was not idempotent"

log "user scope: status + doctor"
homonto status
homonto doctor 2>&1 | tee /tmp/doctor.out
grep -q 'ok: skill "demo" linked (opencode)' /tmp/doctor.out || fail "doctor did not confirm opencode link"

# ------------------------------------------------------------- project scope
log "project scope: apply relocates links into the repo"
printf '[skills.demo]\nsource = "local:demo"\nscope = "project"\n' > homonto.toml
homonto apply --yes

[ -L "$OPEN_PROJ" ]   || fail "opencode project link not created"
# ADR 0026: a same-domain project link carries a RELATIVE target; the
# invariant is that it resolves to the content, not its spelling.
[ -d "$OPEN_PROJ" ] || fail "project link does not resolve to content"
case "$(readlink "$OPEN_PROJ")" in
  /*) fail "same-domain project link must be relative, got $(readlink "$OPEN_PROJ")" ;;
esac
# The old user-scope link must have been pruned — no orphan.
[ -e "$OPEN_USER" ]   && fail "user link not pruned after switch to project"

log "project scope: second apply is idempotent"
out="$(homonto apply --yes 2>&1)"
printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second project apply was not idempotent"

log "project scope: doctor checks the project location"
homonto doctor 2>&1 | tee /tmp/doctor2.out
grep -q 'ok: skill "demo" linked (opencode)' /tmp/doctor2.out || fail "doctor did not confirm opencode project link"

# --------------------------------------------- MCP + settings + secret refs
# Assertions here are against the projected FILES and the state file, not against
# homonto's stdout — the real proof that projection and secret resolution work.
log "mcp + settings + secret: apply projects into opencode"
MWORK="$(mktemp -d)"; cd "$MWORK"
SECRET_VALUE="smoke_do_not_leak_v4"
export SMOKE_SECRET="$SECRET_VALUE"
cat > homonto.toml <<'EOF'
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]
env = { API_KEY = "${SMOKE_SECRET}" }

[settings.opencode]
theme = "opencode-dark"
EOF
homonto apply --yes

OJSONC="$HOME/.config/opencode/opencode.jsonc"
MSTATE="$MWORK/.homonto/state.json"

# OpenCode MCP server projected into opencode.jsonc, with the secret RESOLVED.
grep -q 'codegraph' "$OJSONC"         || fail "opencode mcp server not projected into opencode.jsonc"
grep -q '\["codegraph", "serve", "--mcp"\]' "$OJSONC" || fail "opencode mcp command not projected"
grep -q "$SECRET_VALUE" "$OJSONC"     || fail "secret env ref not resolved into opencode.jsonc"
# OpenCode setting projected into the same file, top level.
grep -q 'opencode-dark' "$OJSONC"     || fail "opencode setting not projected"
# The state file must record the REFERENCE, never the resolved secret value.
grep -q 'SMOKE_SECRET' "$MSTATE"      || fail "state did not record the secret reference"
if grep -q "$SECRET_VALUE" "$MSTATE"; then fail "state LEAKED the resolved secret value"; fi

log "mcp + settings: second apply is idempotent"
out="$(homonto apply --yes 2>&1)"; printf '%s\n' "$out"
printf '%s' "$out" | grep -q "No changes" || fail "second mcp/settings apply was not idempotent"

# ------------------------------------------------------------- init command
log "init: scaffolds a fresh repo"
IWORK="$(mktemp -d)"
homonto init "$IWORK"
[ -f "$IWORK/homonto.toml" ]                 || fail "init did not create homonto.toml"
[ -f "$IWORK/.gitignore" ]                   || fail "init did not create .gitignore"
[ -f "$IWORK/homonto/skills/.gitkeep" ]      || fail "init did not create homonto/skills"

# ----------------------------------------------- conflict smoke (skill dirs)
# A real file or a foreign symlink where a skill link would go is user-owned:
# apply must ABORT and leave it byte-for-byte / target unchanged.
log "conflict: a real file at a skill dst aborts apply and is preserved"
XWORK="$(mktemp -d)"; cd "$XWORK"
mkdir -p homonto/skills/blocker
printf 'skill body\n' > homonto/skills/blocker/SKILL.md
printf '[skills.blocker]\nsource = "local:blocker"\nscope = "user"\n' > homonto.toml
mkdir -p "$HOME/.config/opencode/skills"
printf 'user data\n' > "$HOME/.config/opencode/skills/blocker"
if homonto apply --yes >/dev/null 2>&1; then fail "apply must abort on a real file at the skill dst"; fi
grep -q 'user data' "$HOME/.config/opencode/skills/blocker" || fail "apply clobbered the user's real file"
rm -f "$HOME/.config/opencode/skills/blocker"

log "conflict: a foreign symlink at a skill dst aborts apply and is unchanged"
FOREIGN="$(mktemp -d)"
ln -s "$FOREIGN" "$HOME/.config/opencode/skills/blocker"
if homonto apply --yes >/dev/null 2>&1; then fail "apply must abort on a foreign symlink at the skill dst"; fi
[ "$(readlink "$HOME/.config/opencode/skills/blocker")" = "$FOREIGN" ] || fail "apply changed the foreign symlink"
rm -f "$HOME/.config/opencode/skills/blocker"

printf '\nSMOKE PASS\n'
