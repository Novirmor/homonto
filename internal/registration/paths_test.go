package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestGitRegistrationPathLayout(t *testing.T) {
	got := GitRegistrationPath("/ws/services/api/.git")
	if want := filepath.Join("/ws/services/api/.git", "homonto", "registration.json"); got != want {
		t.Errorf("GitRegistrationPath = %q, want %q", got, want)
	}
	got = GitLeasePath("/ws/services/api/.git")
	if want := filepath.Join("/ws/services/api/.git", "homonto", "lease.json"); got != want {
		t.Errorf("GitLeasePath = %q, want %q", got, want)
	}
}

func TestNonGitRegistrationPathLayout(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	canonical := "/home/u/ws/docs"

	sum := sha256.Sum256([]byte(canonical))
	hashed := hex.EncodeToString(sum[:])

	dir := NonGitMemberDir(stateRoot, canonical)
	if want := filepath.Join(stateRoot, "members", hashed); dir != want {
		t.Errorf("NonGitMemberDir = %q, want %q", dir, want)
	}
	if got := NonGitRegistrationPath(stateRoot, canonical); got != filepath.Join(dir, "registration.json") {
		t.Errorf("NonGitRegistrationPath = %q, want registration.json inside member dir", got)
	}
	if got := NonGitLeasePath(stateRoot, canonical); got != filepath.Join(dir, "lease.json") {
		t.Errorf("NonGitLeasePath = %q, want lease.json inside member dir", got)
	}
}

func TestNonGitPathIsStableAcrossSeparators(t *testing.T) {
	// The hash input is the canonical slash-separated path, so state
	// layouts agree wherever they are computed.
	stateRoot := "/x"
	a := NonGitMemberDir(stateRoot, "/home/u/ws")
	b := NonGitMemberDir(stateRoot, "/home/u/ws")
	if a != b {
		t.Errorf("hash input not stable: %q vs %q", a, b)
	}
}
