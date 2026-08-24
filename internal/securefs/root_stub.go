//go:build !linux && !darwin

package securefs

import (
	"fmt"
	"io/fs"
)

// Non-Unix builds fail closed: the anchor cannot even be opened, and every
// operation reports ErrUnsupported rather than degrading to path-based I/O
// that would forfeit the confinement and durability guarantees.

func openAnchor(path string) (int, error) { return -1, ErrUnsupported }

func closeFd(fd int) error { return nil }

func unsupported(op, rel string) error {
	return fmt.Errorf("securefs: %s %s: %w", op, rel, ErrUnsupported)
}

func (r *Root) ReadFile(rel string) ([]byte, error) { return nil, unsupported("read", rel) }

func (r *Root) WriteAtomic(rel string, data []byte, mode fs.FileMode) error {
	return unsupported("write", rel)
}

func (r *Root) CreateExclusive(rel string, data []byte, mode fs.FileMode) error {
	return unsupported("create", rel)
}

func (r *Root) Rename(oldRel, newRel string) error {
	return unsupported("rename", oldRel+" -> "+newRel)
}

func (r *Root) Remove(rel string) error { return unsupported("remove", rel) }

func (r *Root) SyncDir(rel string) error { return unsupported("sync", rel) }
