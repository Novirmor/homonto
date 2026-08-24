package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

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
// a non-git member at canonicalPath: stateRoot/members/<sha256(path)>.
// canonicalPath must already be canonical; hashing happens here so every
// caller derives the same slot.
func NonGitMemberDir(stateRoot, canonicalPath string) string {
	return filepath.Join(stateRoot, "members", hashPath(canonicalPath))
}

// NonGitRegistrationPath returns the registration file path for a non-git
// member at canonicalPath under stateRoot.
func NonGitRegistrationPath(stateRoot, canonicalPath string) string {
	return filepath.Join(NonGitMemberDir(stateRoot, canonicalPath), registrationName)
}

// NonGitLeasePath returns the lease file path for a non-git member at
// canonicalPath under stateRoot.
func NonGitLeasePath(stateRoot, canonicalPath string) string {
	return filepath.Join(NonGitMemberDir(stateRoot, canonicalPath), leaseName)
}

// hashPath digests a canonical path to its lowercase hex sha256.
func hashPath(canonicalPath string) string {
	sum := sha256.Sum256([]byte(canonicalPath))
	return hex.EncodeToString(sum[:])
}
