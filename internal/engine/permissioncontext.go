package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenCode v1.18.29 reports edits relative to its Git worktree, or the filesystem
// root for a non-Git instance. A source command's cwd does not change that base.
func permissionHostWorktree(directory string) (string, error) {
	cmd := exec.Command("git", "-C", directory, "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err == nil {
		root := strings.TrimSuffix(string(output), "\n")
		if filepath.IsAbs(root) {
			return filepath.Clean(root), nil
		}
		return "", fmt.Errorf("permission host %q: Git returned a non-absolute worktree %q", directory, root)
	}
	if strings.HasPrefix(string(output), "fatal: not a git repository") {
		return filepath.VolumeName(directory) + string(filepath.Separator), nil
	}
	return "", fmt.Errorf("permission host %q: git rev-parse --show-toplevel: %w: %s", directory, err, strings.TrimSpace(string(output)))
}

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
