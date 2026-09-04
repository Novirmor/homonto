// Package destlock is the destination-name reservation shared by `onto new`
// and `to promote` (ADR 0028): both create a directory under docs/changes/,
// and a promotion that renames its result into place must not race a
// concurrent create of the same name. Same O_EXCL protocol as the workspace
// locks: portable, fail-fast, pid-recorded, and a holder provably dead is
// reclaimed automatically (nothing live can be stolen).
package destlock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/noviopenworks/homonto/internal/workcli"
)

// Acquire takes the creation lock for workflow-root/changes under root, returning a
// releaser.
func Acquire(root string) (func(), error) {
	dir := filepath.Join(workcli.WorkflowRootOrDefault(root), "changes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("destlock: %w", err)
	}
	path := filepath.Join(dir, ".new.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		if pid, ok := readPid(path); ok && !pidAlive(pid) {
			if rmErr := os.Remove(path); rmErr == nil {
				f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			}
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("destlock: another change creation is in progress (%s); wait for it or remove the file if none is running", path)
		}
		return nil, fmt.Errorf("destlock: %w", err)
	}
	fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

func readPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(ln, "pid="); ok {
			if pid, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && pid > 0 {
				return pid, true
			}
		}
	}
	return 0, false
}

func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return !(errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH))
	}
	return true
}
