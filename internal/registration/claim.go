package registration

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/securefs"
)

// fileMode is the permission of every registration file.
const fileMode fs.FileMode = 0o644

// Claim establishes ownership of a member by atomically creating the
// registration file at path (create-if-absent, O_EXCL semantics). securefs
// never creates directories, so Claim mkdirs the parent chain itself.
//
// The path must be the registration slot derived from reg's own content:
// a non-git member claims <state base>/homonto/members/<sha256(member
// root)>/registration.json, a git member claims
// <git-common-dir>/homonto/registration.json. The non-git slot is verified
// by hash; the git slot is verified by shape (…/homonto/registration.json)
// because the git common dir of an arbitrary member root cannot be derived
// without running git — callers derive it from gitx.Inspect of
// reg.MemberRoot via GitRegistrationPath. A path that is not the member's
// slot is ErrInvalidRegistration, so a registration can never be written
// where Locate would not find it. If any registration is already present
// it is rejected — even when idle and even when it names the same
// workspace; releasing or moving an existing claim is Detach's and
// TakeOwnership's job, never a silent side effect of claiming.
func Claim(path string, reg Registration) error {
	if err := verifySlot(path, reg); err != nil {
		return err
	}
	data, err := reg.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("registration: mkdir %s: %w", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()

	err = root.CreateExclusive(filepath.Base(path), data, fileMode)
	if errors.Is(err, fs.ErrExist) {
		existing, rerr := Read(path)
		if rerr != nil {
			return fmt.Errorf("registration: %s: already present and unreadable: %v: %w", path, rerr, ErrOwnedByOther)
		}
		return ownedByOther(path, existing)
	}
	return err
}

// verifySlot checks that path is the registration slot this registration
// content derives: the file name, the slot family matching the member
// kind, and — for non-git members, where no runner is needed — the exact
// path-hash binding.
func verifySlot(path string, reg Registration) error {
	if filepath.Base(path) != registrationName {
		return slotMismatch(path, reg, fmt.Sprintf("file name must be %q", registrationName))
	}
	dir := filepath.Dir(path)
	if reg.Kind == "git" {
		// Git slots live at <git-common-dir>/homonto/registration.json.
		// The common dir itself is runner-derived (caller contract, see
		// Claim); here the shape is checked.
		if base := filepath.Base(dir); base != "homonto" {
			return slotMismatch(path, reg, fmt.Sprintf("git slot must be <git-common-dir>/%s/%s", "homonto", registrationName))
		}
		return nil
	}
	want := hashPath(reg.MemberRoot)
	if filepath.Base(dir) != want ||
		filepath.Base(filepath.Dir(dir)) != "members" ||
		filepath.Base(filepath.Dir(filepath.Dir(dir))) != "homonto" {
		return slotMismatch(path, reg,
			fmt.Sprintf("non_git slot must be <state-base>/homonto/members/%s/%s for member root %s",
				want, registrationName, reg.MemberRoot))
	}
	return nil
}

// slotMismatch renders the expected-vs-actual slot error.
func slotMismatch(path string, reg Registration, expected string) error {
	return fmt.Errorf("registration: member %s (%s) cannot claim slot %s: %s: %w",
		reg.MemberRoot, reg.Kind, path, expected, ErrInvalidRegistration)
}

// Read loads and strictly decodes the registration at path. A missing
// file — including a missing registration directory — is
// ErrNotRegistered.
func Read(path string) (Registration, error) {
	data, err := readRaw(path)
	if err != nil {
		return Registration{}, err
	}
	reg, err := ReadBytes(data)
	if err != nil {
		return Registration{}, fmt.Errorf("%s: %w", path, err)
	}
	return reg, nil
}

// readRaw reads the undecoded bytes of the registration file at path.
func readRaw(path string) ([]byte, error) {
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, notRegistered(path)
		}
		return nil, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Base(path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, notRegistered(path)
		}
		return nil, err
	}
	return data, nil
}

// notRegistered builds the ErrNotRegistered error for path.
func notRegistered(path string) error {
	return fmt.Errorf("registration: %s: %w", path, ErrNotRegistered)
}

// Detach removes the registration at path, but only when it belongs to the
// expected workspace. A foreign registration is never removed.
func Detach(path string, expected identity.WorkspaceID) error {
	reg, err := Read(path)
	if err != nil {
		return err
	}
	if reg.WorkspaceID != expected {
		return ownedByOther(path, reg)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.Base(path)); err != nil {
		return fmt.Errorf("registration: remove %s: %w", path, err)
	}
	return nil
}

// TakeOwnership replaces the registration at path with next
// (detach/attach --take-ownership semantics). It is refused outright
// while a lease file exists next to the registration (the lease's
// presence is statted here, never taken on the caller's word), and it is
// refused when the existing registration names a different workspace:
// only the owning workspace may retarget its own claim. The
// read→check→write window is narrowed by re-reading the file immediately
// before the write; if it changed in between, the takeover is retried
// once and then fails with ErrRegistrationChanged.
func TakeOwnership(path string, next Registration) error {
	return takeOwnership(path, next, readRaw)
}

// takeOwnership is TakeOwnership with the raw-file read injected so the
// change-between-reads path is deterministically testable.
func takeOwnership(path string, next Registration, read func(string) ([]byte, error)) error {
	data, err := next.Marshal()
	if err != nil {
		return err
	}
	var changed error
	for attempt := 0; attempt < 2; attempt++ {
		if err := leasePresent(path); err != nil {
			return err
		}
		first, err := read(path)
		if err != nil {
			return err
		}
		existing, err := ReadBytes(first)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if existing.WorkspaceID != next.WorkspaceID {
			return ownedByOther(path, existing)
		}
		second, err := read(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(first, second) {
			changed = fmt.Errorf("registration: %s: changed during takeover: %w", path, ErrRegistrationChanged)
			continue
		}
		root, err := securefs.OpenRoot(filepath.Dir(path))
		if err != nil {
			return err
		}
		if err := root.WriteAtomic(filepath.Base(path), data, fileMode); err != nil {
			root.Close()
			return fmt.Errorf("registration: take ownership of %s: %w", path, err)
		}
		if err := root.Close(); err != nil {
			return err
		}
		return nil
	}
	return changed
}

// leasePresent reports whether a lease file sits next to the registration
// at path; its presence blocks takeover.
func leasePresent(path string) error {
	_, err := os.Stat(filepath.Join(filepath.Dir(path), leaseName))
	if err == nil {
		return fmt.Errorf("registration: %s: %s present: %w", path, leaseName, ErrLeaseActive)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("registration: stat %s: %w", filepath.Join(filepath.Dir(path), leaseName), err)
}

// ownedByOther builds the rejection error naming the owning workspace.
func ownedByOther(path string, existing Registration) error {
	return fmt.Errorf("registration: %s: owned by workspace %s (repository %s, control %s): %w",
		path, existing.WorkspaceID, existing.RepositoryID, existing.ControlRoot, ErrOwnedByOther)
}
