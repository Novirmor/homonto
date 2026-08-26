package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
)

// ErrInvalidStateRoot rejects a state root that is not the platform state
// base: empty, or one that already carries the homonto component (which
// the slot functions append themselves).
var ErrInvalidStateRoot = errors.New("registration: invalid state root")

// registrationName and leaseName are the file names inside every
// registration directory, whichever side of the git/non-git split it lives
// on. Lease content is owned by the lease layer (Task 2); here the lease
// file's presence is only a takeover blocker.
const (
	registrationName = "registration.json"
	leaseName        = "lease.json"
)

// GitRegistrationPath returns the registration file path for a git member
// whose git common directory is commonDir.
func GitRegistrationPath(commonDir string) string {
	return filepath.Join(commonDir, "homonto", registrationName)
}

// GitLeasePath returns the lease file path for a git member whose git
// common directory is commonDir.
func GitLeasePath(commonDir string) string {
	return filepath.Join(commonDir, "homonto", leaseName)
}

// NonGitMemberDir returns the state directory holding the registration for
// a non-git member at canonicalPath:
// stateRoot/homonto/members/<sha256(canonicalPath)>. stateRoot is the
// platform state BASE (DefaultStateRoot's result — the functions here
// append the homonto component), so a root that already ends in homonto
// is rejected as off-layout rather than silently doubled.
func NonGitMemberDir(stateRoot, canonicalPath string) (string, error) {
	if err := validateStateRoot(stateRoot); err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "homonto", "members", hashPath(canonicalPath)), nil
}

// NonGitRegistrationPath returns the registration file path for a non-git
// member at canonicalPath under the state base stateRoot.
func NonGitRegistrationPath(stateRoot, canonicalPath string) (string, error) {
	dir, err := NonGitMemberDir(stateRoot, canonicalPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, registrationName), nil
}

// NonGitLeasePath returns the lease file path for a non-git member at
// canonicalPath under the state base stateRoot.
func NonGitLeasePath(stateRoot, canonicalPath string) (string, error) {
	dir, err := NonGitMemberDir(stateRoot, canonicalPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, leaseName), nil
}

// validateStateRoot enforces the state-root contract: non-empty, and not
// already carrying the homonto component the slot functions append.
func validateStateRoot(stateRoot string) error {
	if stateRoot == "" {
		return fmt.Errorf("registration: state root must not be empty: %w", ErrInvalidStateRoot)
	}
	if base := filepath.Base(filepath.Clean(stateRoot)); base == "homonto" {
		return fmt.Errorf("registration: state root %s already ends in %q; pass the platform state base (DefaultStateRoot result) instead: %w",
			stateRoot, "homonto", ErrInvalidStateRoot)
	}
	return nil
}

// hashPath digests a canonical path (cleaned with slash semantics, so a
// trailing-slash variant of the same directory hashes identically) to its
// lowercase hex sha256.
func hashPath(canonicalPath string) string {
	sum := sha256.Sum256([]byte(path.Clean(canonicalPath)))
	return hex.EncodeToString(sum[:])
}
