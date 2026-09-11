// Package applylock provides process-released mutation locks.
//
// A current holder takes a kernel lock on a persistent guardian in an owned
// metadata directory and publishes a hard-link claim at the historical O_EXCL
// pathname. The claim keeps old binaries out while the guardian lets a new
// process recover safely after a killed holder.
package applylock

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// lockName is the legacy O_EXCL basename under a project's .homonto directory.
const lockName = "apply.lock"

// GuardianSuffix identifies guardian files. Migration planning still ignores
// historical local guardian artifacts from an earlier implementation.
const GuardianSuffix = ".guardian"

const guardianDirectory = "lock-guardians"

var ErrHeld = errors.New("another apply is in progress")

// Lock is a held mutation lock. Process-lock guardians remain after release;
// their kernel hold is released when a process dies.
type Lock struct {
	path            string
	claimPath       string
	f               *os.File
	removeOnRelease bool
}

// GuardianPath returns the persistent guardian location for legacyPath below
// guardianRoot. guardianRoot must be an existing owned metadata directory.
// Both inputs are canonicalized through their real parent directories, so
// relative paths and symlink aliases name the same guardian.
func GuardianPath(guardianRoot, legacyPath string) (string, error) {
	root, err := canonicalGuardianRoot(guardianRoot)
	if err != nil {
		return "", err
	}
	legacyPath, err = canonicalLegacyPath(legacyPath)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(legacyPath))
	return filepath.Join(root, guardianDirectory, fmt.Sprintf("%x%s", digest, GuardianSuffix)), nil
}

// Acquire takes the project apply guardian under dir. Current callers share
// the process lock and legacy binaries see an O_EXCL-compatible claim.
func Acquire(dir string) (*Lock, error) {
	return AcquirePath(filepath.Join(dir, lockName), dir)
}

// AcquireProcess remains the explicit name used by existing callers. It is
// intentionally the same protocol as Acquire so the two can never split.
func AcquireProcess(dir string) (*Lock, error) { return Acquire(dir) }

// AcquirePath guards one legacy O_EXCL pathname. guardianRoot is a stable,
// owned metadata directory (for example .homonto or a Git-private directory),
// never a process-global cache. The legacy lock's parent must already exist;
// callers validate or create their own records directories before locking.
func AcquirePath(path, guardianRoot string) (*Lock, error) {
	if err := PrepareGuardianRoot(guardianRoot); err != nil {
		return nil, err
	}
	guardianPath, err := GuardianPath(guardianRoot, path)
	if err != nil {
		return nil, err
	}
	legacyPath, err := canonicalLegacyPath(path)
	if err != nil {
		return nil, err
	}
	return acquirePath(legacyPath, guardianPath)
}

// PrepareGuardianRoot establishes the owned metadata directory that stores
// persistent guardian files. Workspace initialization calls it before ordinary
// workflow mutations so the lock implementation never needs a global cache.
func PrepareGuardianRoot(path string) error {
	if err := ensureGuardianRoot(path); err != nil {
		return err
	}
	path, err := canonicalGuardianRoot(path)
	if err != nil {
		return err
	}
	return ensureGuardianDirectory(filepath.Join(path, guardianDirectory))
}

func ensureGuardianRoot(path string) error {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		parent, parentErr := canonicalExistingDirectory(filepath.Dir(path), "guardian root parent")
		if parentErr != nil {
			return parentErr
		}
		path = filepath.Join(parent, filepath.Base(path))
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("apply lock: create guardian root: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("apply lock: guardian root is not a real directory")
	}
	_, err = canonicalGuardianRoot(path)
	return err
}

func ensureGuardianDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("apply lock: create guardian directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("apply lock: guardian directory is not a real directory")
	}
	return nil
}

func canonicalLegacyPath(path string) (string, error) {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	if filepath.Base(path) == string(filepath.Separator) {
		return "", fmt.Errorf("apply lock: invalid lock path")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("apply lock: legacy lock path must not be a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("apply lock: inspect legacy lock: %w", err)
	}
	parent, err := canonicalExistingDirectory(filepath.Dir(path), "legacy lock parent")
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func canonicalExistingDirectory(path, label string) (string, error) {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("apply lock: resolve %s: %w", label, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("apply lock: inspect %s: %w", label, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("apply lock: %s is not a directory", label)
	}
	return filepath.Clean(path), nil
}

func canonicalGuardianRoot(path string) (string, error) {
	path, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("apply lock: inspect guardian root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("apply lock: guardian root is not a real directory")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("apply lock: resolve guardian root: %w", err)
	}
	return filepath.Clean(path), nil
}

func absoluteCleanPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("apply lock: invalid lock path")
	}
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("apply lock: resolve path: %w", err)
	}
	return path, nil
}

func guardianHeader(legacyPath string) string {
	digest := sha256.Sum256([]byte(legacyPath))
	return fmt.Sprintf("homonto-process-guardian-v2\n%x\n", digest)
}

// Release removes this process's verified legacy claim while its guardian is
// still kernel-locked, then releases the kernel hold. A foreign or replaced
// claim is never removed.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	if l.f != nil {
		f := l.f
		l.f = nil
		claimErr := removeVerifiedClaim(f, l.claimPath)
		return errors.Join(claimErr, f.Close())
	}
	if !l.removeOnRelease {
		return nil
	}
	l.removeOnRelease = false
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("apply lock release: %w", err)
	}
	return nil
}

func removeVerifiedClaim(f *os.File, path string) error {
	if path == "" {
		return nil
	}
	guardian, err := f.Stat()
	if err != nil {
		return fmt.Errorf("apply lock release: inspect guardian: %w", err)
	}
	claim, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("apply lock release: legacy claim disappeared at %s", path)
	}
	if err != nil {
		return fmt.Errorf("apply lock release: inspect legacy claim: %w", err)
	}
	if !claim.Mode().IsRegular() || !os.SameFile(guardian, claim) {
		return fmt.Errorf("apply lock release: legacy claim at %s was replaced; preserving it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("apply lock release: remove legacy claim: %w", err)
	}
	return nil
}
