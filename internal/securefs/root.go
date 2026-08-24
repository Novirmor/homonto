// Package securefs provides a root-confined, crash-durable filesystem layer
// for Homonto's control plane. Every operation is anchored to the directory
// file descriptor opened by OpenRoot and reaches files only through
// openat-style traversal with O_NOFOLLOW on each component, so a planted
// symlink can never redirect a control-plane access outside the anchor.
//
// Paths are root-relative and slash-clean: absolute paths, empty components,
// "." and "..", backslashes, and NUL bytes are rejected before any syscall.
// Symlinks are rejected everywhere they would be resolved — as a parent
// component or as the final component of a read or write. The one
// deliberate exception is Remove, which unlinks a final symlink itself and
// can never touch its target; Rename likewise moves a symlink as a link.
//
// Writes are durable and atomic: content lands in a uniquely named
// temporary file inside the destination directory, is fsynced, and is
// renamed over the destination, after which the directory is fsynced. After
// a crash the destination therefore holds either the old or the new
// content, never a partial file, and no temp file survives a completed or
// refused operation.
//
// The anchor path given to OpenRoot is trusted: it may itself contain
// symlinks (the OS resolves it at open time), but nothing below it is ever
// resolved through a symlink. A Root is safe for concurrent use by multiple
// goroutines; Close must not race with in-flight operations.
//
// Only Linux and Darwin are supported; on every other platform all
// operations fail closed with ErrUnsupported.
package securefs

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrUnsupported is returned by every operation on platforms that lack the
// fd-anchored syscalls this package requires. Non-Unix builds fail closed.
var ErrUnsupported = errors.New("securefs: unsupported platform")

// tmpPrefix is the name prefix of temporary files created for atomic
// writes; the remainder is random, and no such file survives an operation.
const tmpPrefix = ".securefs-tmp-"

// Root is an open directory anchor. All operations interpret their rel
// argument relative to this directory and cannot escape it, because
// traversal is fd-anchored and refuses symlinked components.
type Root struct {
	fd    int
	path  string
	once  sync.Once
	close error
}

// OpenRoot opens path as the anchor directory for a Root. The path itself
// is resolved by the operating system (it may traverse symlinks); it must
// name a directory. Callers must Close the Root when done.
func OpenRoot(path string) (*Root, error) {
	if path == "" {
		return nil, fmt.Errorf("securefs: root path must not be empty")
	}
	fd, err := openAnchor(path)
	if err != nil {
		return nil, fmt.Errorf("securefs: open root %s: %w", path, err)
	}
	return &Root{fd: fd, path: path}, nil
}

// Close releases the anchor. It is idempotent. Operations after Close fail.
func (r *Root) Close() error {
	r.once.Do(func() { r.close = closeFd(r.fd) })
	return r.close
}

// validateRel checks that rel is a root-relative slash-clean path — no
// leading slash, no empty, ".", or ".." components, no backslashes, no NUL
// — and returns its components.
func validateRel(rel string) ([]string, error) {
	if rel == "" {
		return nil, fmt.Errorf("securefs: relative path must not be empty")
	}
	if strings.HasPrefix(rel, "/") {
		return nil, fmt.Errorf("securefs: path %q must be relative to the root, not absolute", rel)
	}
	if strings.ContainsRune(rel, '\\') {
		return nil, fmt.Errorf("securefs: path %q must use '/' as its only separator", rel)
	}
	if strings.ContainsRune(rel, 0) {
		return nil, fmt.Errorf("securefs: path %q contains a NUL byte", rel)
	}
	comps := strings.Split(rel, "/")
	for _, c := range comps {
		switch c {
		case "":
			return nil, fmt.Errorf("securefs: path %q contains an empty component", rel)
		case ".", "..":
			return nil, fmt.Errorf("securefs: path %q contains %q", rel, c)
		}
	}
	return comps, nil
}

// splitFileRel validates rel as a file path and splits it into the parent
// directory components and the final component.
func splitFileRel(rel string) (parent []string, base string, err error) {
	comps, err := validateRel(rel)
	if err != nil {
		return nil, "", err
	}
	return comps[:len(comps)-1], comps[len(comps)-1], nil
}
