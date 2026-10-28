package engine

import "path/filepath"

// Materialization may not have created the leaf yet. Resolve existing ancestors
// so a first apply and its repeat grant exactly the same bounded content root.
func permissionCanonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(permissionCanonicalPath(parent), filepath.Base(path))
}
