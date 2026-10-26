package agentfm

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	embedded "github.com/noviopenworks/homonto/catalog"
)

// Role is a builtin workflow subagent's place in the division of labor, read
// from its own capability intent in the embedded catalog.
type Role struct {
	// Coordinator is the primary writable agent that owns workflow records.
	Coordinator bool
	// Writer edits sources but never the coordinator's records.
	Writer bool
}

var builtinRoles = sync.OnceValues(func() (map[string]Role, error) {
	entries, err := fs.ReadDir(embedded.FS, "subagents")
	if err != nil {
		return nil, err
	}
	roles := map[string]Role{}
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".md")
		if !ok || entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(embedded.FS, path.Join("subagents", entry.Name()))
		if err != nil {
			return nil, err
		}
		fm, _, ok := split(data)
		if !ok {
			return nil, fmt.Errorf("agentfm: builtin subagent %q has no frontmatter", name)
		}
		h, present, err := parseHomonto(fm)
		if err != nil || !present {
			return nil, fmt.Errorf("agentfm: builtin subagent %q needs a valid homonto block: %v", name, err)
		}
		roles[name] = Role{Coordinator: h.Primary && !h.ReadOnly, Writer: !h.Primary && !h.ReadOnly}
	}
	return roles, nil
})

// BuiltinRoles returns the embedded catalog's subagents by name. Only these
// receive workflow tool rules and workspace directory rules, so adding a
// bundled role needs no list edits, and other agents cannot claim either.
func BuiltinRoles() (map[string]Role, error) { return builtinRoles() }
