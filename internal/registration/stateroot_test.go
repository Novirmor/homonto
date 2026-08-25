package registration

import (
	"path/filepath"
	"strings"
	"testing"
)

// A non-Git member's slot has nowhere to live beside the member itself, so
// it goes in the machine's state directory — under the user's home by
// default. That is right for a person's installation and wrong everywhere
// else: without an override, a test suite or a CI runner writes into
// whichever home the process happens to have and leaves it there.

// TestStateRootHonoursTheOverride.
func TestStateRootHonoursTheOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv(StateRootEnv, want)
	got, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if got != want {
		t.Errorf("StateRoot() = %q, want the override %q", got, want)
	}
}

// TestStateRootFallsBackToThePlatformDefault.
func TestStateRootFallsBackToThePlatformDefault(t *testing.T) {
	t.Setenv(StateRootEnv, "")
	got, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	want, err := DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if got != want {
		t.Errorf("StateRoot() = %q, want the platform default %q", got, want)
	}
}

// TestStateRootRefusesARelativeOverride. A relative path would resolve
// against whatever directory the process happens to be in, so the same
// setting would name different slots for the same member.
func TestStateRootRefusesARelativeOverride(t *testing.T) {
	t.Setenv(StateRootEnv, "state")
	if _, err := StateRoot(); err == nil {
		t.Fatal("a relative state root was accepted")
	}
}

// TestTheOverrideActuallyMovesASlot proves the override reaches the paths
// that matter, not just the accessor.
func TestTheOverrideActuallyMovesASlot(t *testing.T) {
	base := t.TempDir()
	t.Setenv(StateRootEnv, base)
	root, err := StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	slot, err := NonGitLeasePath(root, filepath.Join(t.TempDir(), "assets"))
	if err != nil {
		t.Fatalf("NonGitLeasePath: %v", err)
	}
	if !strings.HasPrefix(slot, base+string(filepath.Separator)) {
		t.Errorf("slot %q is not under the override %q", slot, base)
	}
}
