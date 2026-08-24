//go:build linux

package registration

import (
	"path/filepath"
	"testing"
)

func TestDefaultStateRootLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	// DefaultStateRoot returns the state BASE; the path functions append
	// the homonto component themselves.
	got, err := DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if want := filepath.Join(home, ".local", "state"); got != want {
		t.Errorf("DefaultStateRoot = %q, want %q", got, want)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err = DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if want := xdg; got != want {
		t.Errorf("DefaultStateRoot = %q, want %q", got, want)
	}
}

// TestDefaultStateRootComposesWithSlotLayout pins the end-to-end layout:
// the base from DefaultStateRoot plus the homonto component appended by
// the slot functions.
func TestDefaultStateRootComposesWithSlotLayout(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)

	base, err := DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	dir, err := NonGitMemberDir(base, "/home/u/ws/docs")
	if err != nil {
		t.Fatalf("NonGitMemberDir: %v", err)
	}
	if want := filepath.Join(xdg, "homonto", "members"); filepath.Dir(dir) != want {
		t.Errorf("members dir = %q, want %q", filepath.Dir(dir), want)
	}
}
