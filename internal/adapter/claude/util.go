package claude

import (
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/baseadapter"
)

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
func trim(s, p string) string    { return strings.TrimPrefix(s, p) }

// filePrefix reports whether a state key is a file-projection namespace pruned
// by the generic delete loop. The structured-document prefixes (mcp./setting./
// plugin./pluginconfig./marketplace.) are pruned by their structproj.Project
// calls instead, so they are excluded here to avoid a double delete.
func filePrefix(k string) bool {
	return baseadapter.HasAnyPrefix(k, "skill.", "command.", "subagent.")
}

// filterDesired returns the subset of desired values whose keys are in prefix,
// so each structproj namespace sees only the keys it owns.
func filterDesired(m map[string]string, prefix string) map[string]string {
	return baseadapter.FilterDesired(m, prefix)
}

// filterChanges returns the subset of changes whose keys are in prefix, so each
// structproj namespace applies only the changes it owns.
func filterChanges(changes []adapter.Change, prefix string) []adapter.Change {
	return baseadapter.FilterChanges(changes, prefix)
}

// readStandardized reads a managed tool document normalized for the codec
// (missing file = empty object; unparseable = error; non-object root named).
func readStandardized(path string) ([]byte, error) {
	return baseadapter.ReadStandardizedJSON(path)
}
