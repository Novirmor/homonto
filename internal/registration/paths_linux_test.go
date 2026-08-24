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

	got, err := DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if want := filepath.Join(home, ".local", "state", "homonto"); got != want {
		t.Errorf("DefaultStateRoot = %q, want %q", got, want)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	got, err = DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if want := filepath.Join(xdg, "homonto"); got != want {
		t.Errorf("DefaultStateRoot = %q, want %q", got, want)
	}
}
