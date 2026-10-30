package opencode

import (
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/baseadapter"
	"github.com/noviopenworks/homonto/internal/adapter/fileproj"
	"github.com/noviopenworks/homonto/internal/state"
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
// ownSkillLink reports whether a recorded skill or command link points into
// the user's own content directory: a local: declaration from before skills
// and commands became bring-your-own (ADR 0085). Package and builtin links
// point into the materialized catalog instead.
func (a *Adapter) ownSkillLink(st *state.State, key string) bool {
	var dir string
	switch {
	case hasPrefix(key, "skill."):
		dir = filepath.Join(a.Content, "skills")
	case hasPrefix(key, "command."):
		dir = filepath.Join(a.Content, "commands")
	default:
		return false
	}
	e, ok := st.Get("opencode", key)
	if !ok || a.Content == "" {
		return false
	}
	src, ok := fileproj.RecordedSrc(e.Desired)
	if !ok {
		return false
	}
	if strings.HasPrefix(src, filepath.Clean(dir)+string(filepath.Separator)) {
		return true
	}
	// A recorded absolute source still names the old root after a move.
	// Recognize its project-relative content directory, not the current root.
	// This only releases state; it never rewrites or removes the user's link.
	if a.ProjectRoot == "" {
		return false
	}
	rel, err := filepath.Rel(a.ProjectRoot, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return strings.HasSuffix(filepath.Dir(src), string(filepath.Separator)+rel)
}

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
