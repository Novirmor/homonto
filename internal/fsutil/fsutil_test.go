package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestWriteAtomicPreservesExistingMode: a 0600 config file (it may hold
// resolved secrets) must never be loosened by a rewrite.
func TestWriteAtomicPreservesExistingMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(p, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(p, []byte(`{"new":true}`)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("existing 0600 file loosened to %v after write", got)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"new":true}` {
		t.Fatalf("content = %s", b)
	}
}

// TestWriteAtomicWritesThroughSymlink reproduces the verify round's dotfiles
// finding: rename-over-path replaces a symlinked target (~/.claude.json ->
// dotfiles/claude.json) with a regular file, silently diverging from the
// dotfiles copy. The write must land in the link's target, keeping the link.
func TestWriteAtomicWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "claude.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lnk := filepath.Join(dir, ".claude.json")
	if err := os.Symlink(target, lnk); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(lnk, []byte(`{"new":true}`)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(lnk)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced by a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != `{"new":true}` {
		t.Fatalf("link target content = %s, want the new content", b)
	}
}

// TestWriteAtomicNewFileIs0600: files we create may receive resolved secrets
// on a later apply, so the safe default is owner-only.
func TestWriteAtomicNewFileIs0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "cfg.json")
	if err := WriteAtomic(p, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("new file created %v, want 0600", got)
	}
}

func TestRenameDurableSyncsDestinationThenSource(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	destinationDir := filepath.Join(root, "destination")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(sourceDir, "item")
	newPath := filepath.Join(destinationDir, "item")
	if err := os.WriteFile(oldPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	var synced []string
	if err := renameDurable(oldPath, newPath, func(dir string) error {
		synced = append(synced, dir)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{destinationDir, sourceDir}; !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced %v, want %v", synced, want)
	}
}

func TestRenameDurableSyncsSameParentOnce(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old")
	newPath := filepath.Join(dir, "new")
	if err := os.WriteFile(oldPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := renameDurable(oldPath, newPath, func(string) error {
		calls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("sync calls = %d, want 1", calls)
	}
}

func TestRenameDurableSyncFailureLeavesMovedDestination(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	destinationDir := filepath.Join(root, "destination")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(sourceDir, "item")
	newPath := filepath.Join(destinationDir, "item")
	if err := os.WriteFile(oldPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("sync failed")
	if err := renameDurable(oldPath, newPath, func(string) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("renameDurable error = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("destination missing after sync failure: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("source still exists after rename: %v", err)
	}
}
