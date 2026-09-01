// Key- and document-level helpers shared by the Claude and OpenCode adapters.
// Each adapter keeps a local name delegating here, so the per-tool files stay
// terse while the logic has one owner — the same one-owner rule the catalog
// and config packages follow.
package baseadapter

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
)

// HasAnyPrefix reports whether s starts with any of prefixes. The adapters'
// managed-key predicates differ only in their prefix lists; this is the shared
// matcher.
func HasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// MCPProjected reports whether a declared MCP server has something runnable
// to project for tool: it targets the tool (or all tools) and carries a
// command. The per-tool JSON schema stays in each adapter; only the guard is
// shared.
func MCPProjected(m config.MCP, tool string) bool {
	return slices.Contains(m.TargetsOrAll(), tool) && len(m.Command) > 0
}

// FilterDesired returns the subset of desired values whose keys carry prefix,
// so each structproj namespace sees only the keys it owns.
func FilterDesired(m map[string]string, prefix string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out
}

// FilterChanges returns the subset of changes whose keys carry prefix, so each
// structproj namespace applies only the changes it owns.
func FilterChanges(changes []adapter.Change, prefix string) []adapter.Change {
	var out []adapter.Change
	for _, c := range changes {
		if strings.HasPrefix(c.Key, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// ReadStandardizedJSON reads a tool's JSON document normalized for the JSON
// codec. A missing file standardizes to an empty object — the first projection
// into a fresh tool home — anything unparseable is an error (homonto never
// overwrites a file it cannot parse), and a non-object root is reported with
// its path.
func ReadStandardizedJSON(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return jsonutil.Standardize(nil)
	}
	if err != nil {
		return nil, err
	}
	doc, err := jsonutil.Standardize(b)
	if err != nil {
		return nil, err
	}
	if err := jsonutil.ObjectRoot(doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}
