package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigPath returns the workspace manifest path under a control root.
func ConfigPath(controlRoot string) string {
	return filepath.Join(controlRoot, ".homonto", "config.toml")
}

// CanonicalPath returns the absolute, symlink-resolved, cleaned form of
// path. A path that does not exist is returned in its lexical absolute
// form — canonicalization must remain usable for lookups of paths the
// caller is about to create.
//
// Scope normalization is intentionally forked from gitx's samePath
// (internal/gitx/worktree.go): physical resolution with a lexical
// fallback. Drift risk: a change in either normalization that is not
// mirrored here changes what "the same directory" means across packages.
func CanonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("workspace: canonicalize %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Clean(abs), nil
		}
		return "", fmt.Errorf("workspace: canonicalize %s: %w", path, err)
	}
	return resolved, nil
}
