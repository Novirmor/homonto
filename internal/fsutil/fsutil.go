// Package fsutil holds shared filesystem helpers used by adapters and state.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path via a unique temp file in the target
// directory, fsyncing before rename so a crash never leaves a truncated
// file. An existing file keeps its current mode (a user-tightened 0600 is
// never loosened); new files default to 0600 because managed configs may
// receive resolved secrets.
func WriteAtomic(path string, data []byte) error {
	// A symlinked target (e.g. a dotfiles-managed config) must be
	// written through, not replaced: renaming over the link path would swap it
	// for a regular file that silently diverges from the dotfiles copy. Write
	// at the resolved location instead; a missing file resolves to path as-is.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(dir, ".homonto-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once renamed
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FileExists reports whether path exists and is not a directory. Provided as
// the shared helper so the adapter/engine packages do not each declare their
// own four-line wrapper.
func FileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// WriteControlPlane atomically writes homonto's OWN control-plane files under
// .homonto (state, remote lockfile, materialized catalog). Unlike WriteAtomic it
// does NOT follow a symlink at the destination: if the final path component is a
// symlink the write is refused, so a planted link cannot redirect a
// control-plane write outside the project. mode is the perm applied to a newly
// created file; an existing regular file's perm is preserved and never loosened
// (a user-tightened 0600 stays 0600). The write is atomic: a temp file in the
// same directory is fsynced and renamed over the destination, which replaces the
// path itself rather than following it.
//
// This is only for homonto's control-plane files. Tool-config projection writes
// (which may legitimately be user-symlinked into a dotfiles repo) keep
// WriteAtomic's follow-through behavior.
func WriteControlPlane(path string, data []byte, mode os.FileMode) error {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fsutil: refusing to write control-plane file through a symlink: %s", path)
		}
		mode = fi.Mode().Perm() // preserve an existing (possibly tightened) mode
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".homonto-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once renamed
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
