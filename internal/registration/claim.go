package registration

import (
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
// never creates directories, so Claim mkdirs the parent chain itself. If
// any registration is already present it is rejected — even when idle and
// even when it names the same workspace; releasing or moving an existing
// claim is Detach's and TakeOwnership's job, never a silent side effect of
// claiming.
func Claim(path string, reg Registration) error {
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

// Read loads and strictly decodes the registration at path. A missing
// file — including a missing registration directory — is
// ErrNotRegistered.
func Read(path string) (Registration, error) {
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Registration{}, fmt.Errorf("registration: %s: %w", path, ErrNotRegistered)
		}
		return Registration{}, err
	}
	defer root.Close()
	data, err := root.ReadFile(filepath.Base(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Registration{}, fmt.Errorf("registration: %s: %w", path, ErrNotRegistered)
	}
	if err != nil {
		return Registration{}, err
	}
	reg, err := ReadBytes(data)
	if err != nil {
		return Registration{}, fmt.Errorf("%s: %w", path, err)
	}
	return reg, nil
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
// (detach/attach --take-ownership semantics). It is refused outright while
// a lease exists (the caller reports lease presence), and it is refused
// when the existing registration names a different workspace: only the
// owning workspace may retarget its own claim.
func TakeOwnership(path string, next Registration, activeLeaseExists bool) error {
	if activeLeaseExists {
		return fmt.Errorf("registration: %s: %s present: %w", path, leaseName, ErrLeaseActive)
	}
	data, err := next.Marshal()
	if err != nil {
		return err
	}
	existing, err := Read(path)
	if err != nil {
		return err
	}
	if existing.WorkspaceID != next.WorkspaceID {
		return ownedByOther(path, existing)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(path), data, fileMode); err != nil {
		return fmt.Errorf("registration: take ownership of %s: %w", path, err)
	}
	return nil
}

// ownedByOther builds the rejection error naming the owning workspace.
func ownedByOther(path string, existing Registration) error {
	return fmt.Errorf("registration: %s: owned by workspace %s (repository %s, control %s): %w",
		path, existing.WorkspaceID, existing.RepositoryID, existing.ControlRoot, ErrOwnedByOther)
}
