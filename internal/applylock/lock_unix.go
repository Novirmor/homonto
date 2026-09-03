//go:build !windows

package applylock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AcquireProcess takes the project lock with an OS-released process lock
// (flock): a killed holder releases it automatically, which the transactional
// snapshot path needs — a SIGKILLed apply must not permanently block the
// recovery command (ADR 0030). Same lockfile path as Acquire; the two must
// never be mixed on one project, so snapshot mode is all-or-nothing per
// project.
func AcquireProcess(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	path := filepath.Join(dir, lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("another apply is in progress (process lock held at %s)", path)
		}
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	_, _ = f.WriteString(fmt.Sprintf("pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)))
	return &Lock{path: path, f: f}, nil
}
