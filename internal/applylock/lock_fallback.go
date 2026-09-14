//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package applylock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Unsupported platforms retain the portable lock for ordinary commands. The
// schema-2 migration rejects these platforms before taking a mutation lock.
func acquirePath(path, _ string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%w (lock held at %s)", ErrHeld, path)
		}
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	if _, err := f.WriteString("homonto-legacy-lock\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("apply lock: write lock: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("apply lock: close lock: %w", err)
	}
	return &Lock{path: path, removeOnRelease: true}, nil
}
