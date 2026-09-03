package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteControlPlaneRefusesSymlink: a .homonto control-plane path whose final
// component is a symlink must be refused (a planted link must not redirect the
// write outside the project), and the link target must be left unchanged.
func TestWriteControlPlaneRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside", "victim.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`original`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A control-plane path (e.g. .homonto/state.json) planted as a symlink.
	link := filepath.Join(dir, "state.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	err := WriteControlPlane(link, []byte(`hijacked`), 0o600)
	if err == nil {
		t.Fatal("expected WriteControlPlane to refuse a symlinked target, got nil")
	}
	// The symlink is untouched and its target is unchanged.
	fi, lerr := os.Lstat(link)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced instead of being refused")
	}
	if b, _ := os.ReadFile(target); string(b) != `original` {
		t.Fatalf("symlink target was modified: %s", b)
	}
}

// TestWriteControlPlaneNormalWrite: a regular or absent control-plane path is
// written atomically, and a new file gets the requested mode.
func TestWriteControlPlaneNormalWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	if err := WriteControlPlane(p, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("wrote a symlink, want a regular file")
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("new file mode = %v, want 0600", got)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"a":1}` {
		t.Fatalf("content = %s", b)
	}

	// Rewriting an existing 0600 file must preserve its (tightened) mode.
	if err := WriteControlPlane(p, []byte(`{"a":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if got := fi2.Mode().Perm(); got != 0o600 {
		t.Fatalf("existing 0600 file loosened to %v", got)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"a":2}` {
		t.Fatalf("content after rewrite = %s", b)
	}
}

func TestWriteControlPlaneWithinRefusesSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".onto")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".onto", "state.json")
	err := WriteControlPlaneWithin(root, path, []byte(`{"unsafe":true}`), 0o600)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked parent was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "state.json")); !os.IsNotExist(err) {
		t.Fatalf("write escaped through symlinked parent: %v", err)
	}
}
