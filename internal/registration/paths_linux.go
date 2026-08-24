//go:build linux

package registration

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateRoot returns the Linux state base: $XDG_STATE_HOME, or
// ~/.local/state. The slot functions in paths.go append the homonto
// component; do not pre-append it.
func DefaultStateRoot() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return xdg, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("registration: state root: %w", err)
	}
	return filepath.Join(home, ".local", "state"), nil
}
