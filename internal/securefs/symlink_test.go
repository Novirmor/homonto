package securefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestParentSymlinkComponentIsRejected: a symlink anywhere in the parent
// chain confines the whole operation to failure — no follow, no escape.
func TestParentSymlinkComponentIsRejected(t *testing.T) {
	root, dir := newRoot(t)
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	seedFile(t, filepath.Join(real, "secret.bin"), []byte("s3cret"), 0o600)
	if err := os.Symlink("real", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}

	// Control: the direct path resolves through the anchor.
	got, err := root.ReadFile("real/secret.bin")
	if err != nil {
		t.Fatalf("ReadFile(real/secret.bin): %v", err)
	}
	if string(got) != "s3cret" {
		t.Errorf("control read %q, want %q", got, "s3cret")
	}

	ops := map[string]func() error{
		"read":          func() error { _, err := root.ReadFile("alias/secret.bin"); return err },
		"write-atomic":  func() error { return root.WriteAtomic("alias/new.bin", []byte("x"), 0o600) },
		"create-excl":   func() error { return root.CreateExclusive("alias/new.bin", []byte("x"), 0o600) },
		"rename-source": func() error { return root.Rename("alias/secret.bin", "moved.bin") },
		"rename-target": func() error { return root.Rename("moved.bin", "alias/dest.bin") },
		"remove":        func() error { return root.Remove("alias/secret.bin") },
		"sync-dir":      func() error { return root.SyncDir("alias") },
	}
	for name, op := range ops {
		if err := op(); err == nil {
			t.Errorf("%s through symlinked parent: want error, got nil", name)
		}
	}

	// Nothing was created, moved, or removed through the link.
	if _, err := os.Stat(filepath.Join(real, "new.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("write through parent symlink created a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(real, "dest.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("rename through parent symlink created a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "moved.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("rename through parent symlink moved a file: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(real, "secret.bin")); err != nil {
		t.Errorf("secret vanished after refused remove: %v", err)
	}
}

// TestFinalSymlinkComponentIsRejected: reads and writes never resolve a
// symlink at the destination. Remove is the deliberate exception: it
// unlinks the link itself and can never touch the target.
func TestFinalSymlinkComponentIsRejected(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.WriteAtomic("real.bin", []byte("R"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.bin", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if _, err := root.ReadFile("link"); err == nil {
		t.Error("ReadFile of symlink: want error, got nil")
	}
	if err := root.WriteAtomic("link", []byte("X"), 0o600); err == nil {
		t.Error("WriteAtomic onto symlink: want error, got nil")
	}
	if err := root.CreateExclusive("link", []byte("X"), 0o600); err == nil {
		t.Error("CreateExclusive onto symlink: want error, got nil")
	}
	if err := root.SyncDir("link"); err == nil {
		t.Error("SyncDir of symlinked directory name: want error, got nil")
	}
	got, err := root.ReadFile("real.bin")
	if err != nil {
		t.Fatalf("ReadFile(real.bin): %v", err)
	}
	if string(got) != "R" {
		t.Errorf("target content %q, want %q", got, "R")
	}

	if err := root.Remove("link"); err != nil {
		t.Fatalf("Remove of symlink: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("symlink still present after Remove")
	}
	got, err = root.ReadFile("real.bin")
	if err != nil {
		t.Fatalf("ReadFile(real.bin) after link removal: %v", err)
	}
	if string(got) != "R" {
		t.Errorf("Remove followed the link: target content %q, want %q", got, "R")
	}
}

// TestSymlinkEscapeCannotExitAnchor: an absolute symlink pointing outside
// the anchor must be a dead end for every operation, and failed writes
// through it must leave the outside tree untouched.
func TestSymlinkEscapeCannotExitAnchor(t *testing.T) {
	root, dir := newRoot(t)
	outside := t.TempDir()
	seedFile(t, filepath.Join(outside, "secret"), []byte("outer"), 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "esc")); err != nil {
		t.Fatal(err)
	}

	if _, err := root.ReadFile("esc/secret"); err == nil {
		t.Error("ReadFile escaped the anchor through a symlink: want error")
	}
	if err := root.WriteAtomic("esc/planted", []byte("P"), 0o600); err == nil {
		t.Error("WriteAtomic escaped the anchor through a symlink: want error")
	}
	if err := root.CreateExclusive("esc/planted", []byte("P"), 0o600); err == nil {
		t.Error("CreateExclusive escaped the anchor through a symlink: want error")
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "secret" {
		t.Errorf("outside tree changed: %v", entries)
	}
	got, err := os.ReadFile(filepath.Join(outside, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "outer" {
		t.Errorf("outside file content %q, want %q", got, "outer")
	}
}

func TestSymlinkChainIsRejected(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.WriteAtomic("real", []byte("R"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "l1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("l1", filepath.Join(dir, "l2")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.ReadFile("l2"); err == nil {
		t.Error("ReadFile of symlink chain: want error, got nil")
	}
	if err := root.WriteAtomic("l2", []byte("X"), 0o600); err == nil {
		t.Error("WriteAtomic onto symlink chain: want error, got nil")
	}
}

// TestRenameMovesSymlinkItself: rename operates on directory entries, so a
// symlink source moves as a link — its target is untouched and the moved
// link still refuses to resolve.
func TestRenameMovesSymlinkItself(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.WriteAtomic("real", []byte("R"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("l", "l2"); err != nil {
		t.Fatalf("Rename of symlink: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(dir, "l2"))
	if err != nil {
		t.Fatalf("lstat(l2): %v", err)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Error("renamed entry is not a symlink: rename followed the link")
	}
	if target, err := os.Readlink(filepath.Join(dir, "l2")); err != nil || target != "real" {
		t.Errorf("renamed link target %q (err %v), want %q", target, err, "real")
	}
	got, err := root.ReadFile("real")
	if err != nil {
		t.Fatalf("ReadFile(real): %v", err)
	}
	if string(got) != "R" {
		t.Errorf("target content %q after link rename, want %q", got, "R")
	}
	if _, err := root.ReadFile("l2"); err == nil {
		t.Error("ReadFile of moved symlink: want error, got nil")
	}
}
