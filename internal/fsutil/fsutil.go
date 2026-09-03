// Package fsutil holds shared filesystem helpers used by adapters and state.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

// RequireRealParents confines dir to root and refuses symlinks or non-directory
// components along the existing path. Missing trailing components are allowed
// so callers can validate before creating them.
func RequireRealParents(root, dir string) error {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return fmt.Errorf("fsutil: resolving root: %w", err)
	}
	dir, err = filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("fsutil: resolving directory: %w", err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("fsutil: %s is outside root %s", dir, root)
	}
	cur := root
	if err := requireRealDirectory(cur); err != nil {
		return err
	}
	for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
		if component == "" || component == "." {
			continue
		}
		cur = filepath.Join(cur, component)
		info, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("fsutil: %s is not a real directory (symlinked parents are refused)", cur)
		}
	}
	return nil
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("fsutil: %s is not a real directory (symlinked parents are refused)", path)
	}
	return nil
}

// WriteControlPlaneWithin adds parent-path confinement to WriteControlPlane.
// The second check verifies any directories created between the two calls.
func WriteControlPlaneWithin(root, path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := RequireRealParents(root, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := RequireRealParents(root, dir); err != nil {
		return err
	}
	return WriteControlPlane(path, data, mode)
}

// RenameDurable atomically renames oldPath and syncs both directory entries.
// A returned sync error may mean the rename happened; callers must recover from
// either path rather than assuming an error rolled the move back.
func RenameDurable(oldPath, newPath string) error {
	return renameDurable(oldPath, newPath, syncDirectory)
}

func renameDurable(oldPath, newPath string, syncDir func(string) error) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	destinationDir := filepath.Dir(newPath)
	if err := syncDir(destinationDir); err != nil {
		return err
	}
	sourceDir := filepath.Dir(oldPath)
	if filepath.Clean(sourceDir) != filepath.Clean(destinationDir) {
		if err := syncDir(sourceDir); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
