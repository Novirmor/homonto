//go:build darwin

package registration

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultStateRoot returns the macOS state base:
// ~/Library/Application Support. The slot functions in paths.go append
// the homonto component; do not pre-append it.
func DefaultStateRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("registration: state root: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support"), nil
}
