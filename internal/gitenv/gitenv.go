// Package gitenv builds the environment homonto runs Git with. A caller's
// GIT_* variables (GIT_DIR, GIT_INDEX_FILE, GIT_WORK_TREE, ...) must never
// redirect which repository a probe or mutation sees, a probe must not take
// optional index locks, and nothing may stop to prompt for credentials.
package gitenv

import (
	"os"
	"strings"
)

// Env returns the process environment without GIT_* entries, plus
// GIT_OPTIONAL_LOCKS=0 and GIT_TERMINAL_PROMPT=0.
func Env() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}
