# Shared helpers for the real-tool E2E suites. Sourced, not executed.
log()  { printf '\n=== %s ===\n' "$1"; }
pass() { printf '  PASS  %s\n' "$1"; }
fail() { printf '\n  FAIL  %s\n' "$1" >&2; exit 1; }

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
