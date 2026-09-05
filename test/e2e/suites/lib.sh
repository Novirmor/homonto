# Shared helpers for the real-tool E2E suites. Sourced, not executed.
log()  { printf '\n=== %s ===\n' "$1"; }
pass() { printf '  PASS  %s\n' "$1"; }
fail() { printf '\n  FAIL  %s\n' "$1" >&2; exit 1; }

# Assert that a captured output string contains a literal needle.
contains() { # <haystack> <needle> <fail-msg>
  case "$1" in *"$2"*) ;; *) fail "$3" ;; esac
}
