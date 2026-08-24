//go:build linux

package registration

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateRoot returns the Linux state root: $XDG_STATE_HOME/homonto,
// defaulting to ~/.local/state/homonto.
func DefaultStateRoot() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "homonto"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("registration: state root: %w", err)
	}
	return filepath.Join(home, ".local", "state", "homonto"), nil
}
