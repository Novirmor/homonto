//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package applylock

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

func acquirePath(legacyPath, guardianPath string) (*Lock, error) {
	f, err := os.OpenFile(guardianPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w (lock held at %s; guardian held at %s)", ErrHeld, legacyPath, guardianPath)
		}
		return nil, fmt.Errorf("apply lock: %w", err)
	}
	if err := initializeGuardian(f, legacyPath); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := publishLegacyClaim(f, guardianPath, legacyPath); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{path: guardianPath, claimPath: legacyPath, f: f}, nil
}

func initializeGuardian(f *os.File, legacyPath string) error {
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("apply lock: guardian is not a regular file")
	}
	header := guardianHeader(legacyPath)
	if info.Size() == 0 {
		if _, err := f.WriteAt([]byte(header), 0); err != nil {
			return fmt.Errorf("apply lock: initialize guardian: %w", err)
		}
		if err := f.Sync(); err != nil {
			return fmt.Errorf("apply lock: sync guardian: %w", err)
		}
		return nil
	}
	if info.Size() != int64(len(header)) {
		return fmt.Errorf("apply lock: guardian has an unsupported format")
	}
	data := make([]byte, len(header))
	_, err = f.ReadAt(data, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("apply lock: read guardian: %w", err)
	}
	if string(data) != header {
		return fmt.Errorf("apply lock: guardian has an unsupported format")
	}
	return nil
}

func publishLegacyClaim(f *os.File, guardianPath, legacyPath string) error {
	if err := guardianStillMatches(f, guardianPath); err != nil {
		return err
	}
	if err := os.Link(guardianPath, legacyPath); err == nil {
		if err := verifyOwnClaim(f, legacyPath); err != nil {
			return err
		}
		return nil
	} else if errors.Is(err, fs.ErrExist) {
		if err := verifyOwnClaim(f, legacyPath); err == nil {
			return nil
		}
		return legacyClaimError(legacyPath)
	} else if errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("apply lock: guardian and legacy lock are on different filesystems; refusing to split lock ownership")
	} else {
		return fmt.Errorf("apply lock: publish legacy claim: %w", err)
	}
}

func guardianStillMatches(f *os.File, path string) error {
	opened, err := f.Stat()
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian: %w", err)
	}
	named, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian path: %w", err)
	}
	if !named.Mode().IsRegular() || !os.SameFile(opened, named) {
		return fmt.Errorf("apply lock: guardian changed while acquiring")
	}
	return nil
}

func verifyOwnClaim(f *os.File, path string) error {
	guardian, err := f.Stat()
	if err != nil {
		return fmt.Errorf("apply lock: inspect guardian: %w", err)
	}
	claim, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("apply lock: inspect legacy claim: %w", err)
	}
	if !claim.Mode().IsRegular() || !os.SameFile(guardian, claim) {
		return legacyClaimError(path)
	}
	return nil
}

func legacyClaimError(path string) error {
	return fmt.Errorf("%w (legacy O_EXCL lock exists at %s; remove it only after confirming its holder is no longer running)", ErrHeld, path)
}
