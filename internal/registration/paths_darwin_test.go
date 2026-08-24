//go:build darwin

package registration

import (
	"path/filepath"
	"testing"
)

func TestDefaultStateRootDarwin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := DefaultStateRoot()
	if err != nil {
		t.Fatalf("DefaultStateRoot: %v", err)
	}
	if want := filepath.Join(home, "Library", "Application Support", "homonto"); got != want {
		t.Errorf("DefaultStateRoot = %q, want %q", got, want)
	}
}
