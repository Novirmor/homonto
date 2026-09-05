// Package destlock is the destination-name reservation shared by `onto new`
// and `to promote` (ADR 0028): both create a directory under docs/changes/,
// and a promotion that renames its result into place must not race a
// concurrent create of the same name. It uses the shared workspace-lock
// protocol: portable, fail-fast, pid-recorded, and safe stale-lock recovery.
package destlock

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/workcli"
)

// Acquire takes the creation lock for workflow-root/changes under root, returning a
// releaser.
func Acquire(root string) (func(), error) {
	dir := filepath.Join(workcli.WorkflowRootOrDefault(root), "changes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("destlock: %w", err)
	}
	return workcli.LockWorkspace("destlock", filepath.Join(dir, ".new.lock"))
}
