// Package applylock provides a project-scoped exclusive lock so two concurrent
// `homonto apply` runs cannot plan from the same snapshot and race to a
// last-writer-wins outcome on the state and tool files.
//
// The lock is an O_EXCL lockfile at <dir>/apply.lock. O_CREATE|O_EXCL is
// portable (it works the same on Unix and Windows and, per POSIX, fails on a
// symlink so it cannot be redirected) and needs no platform-specific syscalls,
// unlike flock. The tradeoff is that a process killed with SIGKILL before
// Release leaves a stale lockfile; the file records the holder's pid and start
// time so a stale lock can be diagnosed and removed by hand, but reclamation is
// deliberately not automatic (a live apply must never have its lock stolen).
package applylock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// lockName is the fixed lockfile basename under the project's .homonto dir.
const lockName = "apply.lock"

// Lock is a held project apply lock. Release removes the lockfile.
type Lock struct {
	path string
	// f is non-nil when the lock is a held process lock (flock); Release
	// unlocks and closes it. The O_EXCL lockfile mode leaves it nil.
	f *os.File
}

// Acquire takes the project apply lock under dir (created if needed). It fails
// fast if another apply already holds the lock, rather than blocking or racing.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	path := filepath.Join(dir, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("another apply is in progress (lock held at %s); "+
				"wait for it to finish, or remove the file if no apply is running", path)
		}
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	// Record the holder for post-mortem diagnosis of a stale lock. Best effort:
	// the exclusive create already established ownership.
	fmt.Fprintf(f, "pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	_ = f.Close()
	return &Lock{path: path}, nil
}

// Release removes the lockfile. It is safe to call once; a missing file is not
// an error so a double release or a manual cleanup does not fail.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	if l.f != nil {
		// flock releases on close; the lockfile itself stays (it is the flock
		// anchor, not the lock). Removing it would race a waiter's open.
		return l.f.Close()
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("apply lock release: %w", err)
	}
	return nil
}
