//go:build darwin

package registration

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateRoot returns the macOS state root:
// ~/Library/Application Support/homonto.
func DefaultStateRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("registration: state root: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "homonto"), nil
}
