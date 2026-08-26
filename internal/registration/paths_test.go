package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	// Pinned: sha256 of the literal canonical path.
	sum := sha256.Sum256([]byte("/home/u/ws/docs"))
	hashed := hex.EncodeToString(sum[:])

	dir, err := NonGitMemberDir(stateRoot, canonical)
	if err != nil {
		t.Fatalf("NonGitMemberDir: %v", err)
	}
	if want := filepath.Join(stateRoot, "homonto", "members", hashed); dir != want {
		t.Errorf("NonGitMemberDir = %q, want %q", dir, want)
	}
	regPath, err := NonGitRegistrationPath(stateRoot, canonical)
	if err != nil {
		t.Fatalf("NonGitRegistrationPath: %v", err)
	}
	if want := filepath.Join(dir, "registration.json"); regPath != want {
		t.Errorf("NonGitRegistrationPath = %q, want %q", regPath, want)
	}
	leasePath, err := NonGitLeasePath(stateRoot, canonical)
	if err != nil {
		t.Fatalf("NonGitLeasePath: %v", err)
	}
	if want := filepath.Join(dir, "lease.json"); leasePath != want {
		t.Errorf("NonGitLeasePath = %q, want %q", leasePath, want)
	}
}

func TestNonGitStateRootRejectsHomontoSuffix(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state")
	tests := []struct {
		name      string
		stateRoot string
	}{
		{"homonto suffix", filepath.Join(base, "homonto")},
		{"homonto suffix with trailing slash", filepath.Join(base, "homonto") + string(filepath.Separator)},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NonGitMemberDir(tt.stateRoot, "/home/u/ws/docs"); !errors.Is(err, ErrInvalidStateRoot) {
				t.Fatalf("NonGitMemberDir error = %v, want ErrInvalidStateRoot", err)
			}
			if _, err := NonGitRegistrationPath(tt.stateRoot, "/home/u/ws/ws"); !errors.Is(err, ErrInvalidStateRoot) {
				t.Fatalf("NonGitRegistrationPath error = %v, want ErrInvalidStateRoot", err)
			}
			if _, err := NonGitLeasePath(tt.stateRoot, "/home/u/ws/ws"); !errors.Is(err, ErrInvalidStateRoot) {
				t.Fatalf("NonGitLeasePath error = %v, want ErrInvalidStateRoot", err)
			}
		})
	}
}

func TestNonGitPathHashNormalization(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")

	// The hash input is the cleaned canonical path, so a trailing-slash
	// variant of the same directory lands in the identical slot.
	plain, err := NonGitMemberDir(stateRoot, "/home/u/ws/docs")
	if err != nil {
		t.Fatalf("NonGitMemberDir: %v", err)
	}
	slash, err := NonGitMemberDir(stateRoot, "/home/u/ws/docs/")
	if err != nil {
		t.Fatalf("NonGitMemberDir: %v", err)
	}
	if plain != slash {
		t.Errorf("trailing slash variant = %q, want identical slot %q", slash, plain)
	}

	// Golden pin: the literal path hashes to this exact hex.
	want := "4566e069978fad7209a41ad9cd5b9f9442c4c76d2824451185b7e96c438e44c1"
	if got := filepath.Base(plain); got != want {
		t.Errorf("hash dir = %q, want pinned sha256 %q", got, want)
	}
}
