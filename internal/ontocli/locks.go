package ontocli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/workcli"
)

// lockOnto takes the exclusive onto workspace lock
// (<workflow-root>/changes/.onto.lock): every onto command that mutates an
// existing change (advance, set, abandon, close, bypass, merge-deltas) and
// `onto demote` (as the source-side exclusion) hold it, so two writers never
// interleave on the same change. A lock whose recorded pid provably no
// longer runs is reclaimed automatically by the next attempt.
//
// Lock order is global and fixed: to workspace lock → shared destination
// lock → onto workspace lock. Commands holding only this lock acquire it
// alone; only demote holds all three, in that order.
func lockOnto(root string) (func(), error) {
	if err := os.MkdirAll(changesDir(root), 0o755); err != nil {
		return nil, fmt.Errorf("onto: lock: %w", err)
	}
	return workcli.LockWorkspace("onto", filepath.Join(changesDir(root), ".onto.lock"))
}

// lockToWorkspace takes the `to` workspace lock
// (<workflow-root>/tasks/.to.lock) — the same lock the `to` binary's mutating
// commands and `to promote` hold. `onto demote` takes it first (it creates
// in tasks/) so a demotion and a `to new` of the same name cannot race.
func lockToWorkspace(root string) (func(), error) {
	tasks := filepath.Join(workflowRoot(root), "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		return nil, fmt.Errorf("onto: lock: %w", err)
	}
	return workcli.LockWorkspace("to", filepath.Join(tasks, ".to.lock"))
}
