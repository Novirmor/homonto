//go:build !windows

package applylock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireProcessRefusesSymlinkedLockfile(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("unchanged"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, lockName)
	if err := os.Symlink(victim, lockPath); err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireProcess(dir); err == nil {
		_ = lock.Release()
		t.Fatal("AcquireProcess accepted a symlinked lockfile")
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "unchanged" {
		t.Fatalf("victim = %q, %v; want unchanged", got, err)
	}
	info, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatalf("lock symlink was removed: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lock symlink was replaced: mode=%v", info.Mode())
	}
}
