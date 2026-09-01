package opencode

import (
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/baseadapter"
	"github.com/tidwall/gjson"
)

func arrayHas(doc []byte, path, elem string) bool {
	for _, v := range gjson.GetBytes(doc, path).Array() {
		if v.String() == elem {
			return true
		}
	}
	return false
}

func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }
func trim(s, p string) string    { return strings.TrimPrefix(s, p) }

// managedPrefix reports whether a state key is pruned by the generic delete
// loop: plugin.* (bespoke array membership) and the file-projection prefixes.
// The structured-document prefixes (mcp./setting./tui.) are pruned by their
// structproj.Project calls instead, so they are excluded here to avoid a double
// delete.
func managedPrefix(k string) bool {
	return baseadapter.HasAnyPrefix(k, "plugin.", "skill.", "command.", "subagent.")
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
