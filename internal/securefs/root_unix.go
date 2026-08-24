//go:build linux || darwin

package securefs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// openAnchor opens the trusted anchor directory. Its own path is resolved
// by the OS; everything below is confined by fd-anchored traversal.
func openAnchor(path string) (int, error) {
	return unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
}

func closeFd(fd int) error { return unix.Close(fd) }

// pathErr wraps a raw syscall error in a *fs.PathError, converting
// x/sys/unix.Errno to syscall.Errno so sentinels like fs.ErrNotExist match
// through errors.Is.
func pathErr(op, rel string, err error) error {
	var errno unix.Errno
	if errors.As(err, &errno) {
		err = syscall.Errno(errno)
	}
	return &fs.PathError{Op: op, Path: rel, Err: err}
}

// openDir opens the directory addressed by comps with O_NOFOLLOW on every
// component. The caller owns the returned fd unless borrowed is true, which
// means comps was empty and the anchor fd itself was returned.
func (r *Root) openDir(comps []string) (fd int, borrowed bool, err error) {
	if len(comps) == 0 {
		return r.fd, true, nil
	}
	cur := r.fd
	for i, c := range comps {
		next, oerr := unix.Openat(cur, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if oerr != nil {
			if cur != r.fd {
				unix.Close(cur)
			}
			return -1, false, pathErr("openat", strings.Join(comps[:i+1], "/"), oerr)
		}
		if cur != r.fd {
			unix.Close(cur)
		}
		cur = next
	}
	return cur, false, nil
}

// openParent validates rel, opens its parent directory with symlink-free
// traversal, and returns the fd plus the final component.
func (r *Root) openParent(rel string) (dirfd int, base string, borrowed bool, err error) {
	parent, base, err := splitFileRel(rel)
	if err != nil {
		return -1, "", false, err
	}
	dirfd, borrowed, err = r.openDir(parent)
	if err != nil {
		return -1, "", false, err
	}
	return dirfd, base, borrowed, nil
}

// ReadFile returns the content of the regular file at rel. The final
// component is opened O_NOFOLLOW and O_NONBLOCK — the latter so a planted
// fifo or device cannot hang the open — and anything that is not a regular
// file is refused.
func (r *Root) ReadFile(rel string) ([]byte, error) {
	dirfd, base, borrowed, err := r.openParent(rel)
	if err != nil {
		return nil, err
	}
	if !borrowed {
		defer unix.Close(dirfd)
	}
	fd, err := unix.Openat(dirfd, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, pathErr("openat", rel, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, pathErr("fstat", rel, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("securefs: read %s: not a regular file", rel)
	}
	data, rerr := readAll(fd)
	if rerr != nil {
		unix.Close(fd)
		return nil, pathErr("read", rel, rerr)
	}
	if err := unix.Close(fd); err != nil {
		return nil, pathErr("close", rel, err)
	}
	return data, nil
}

// Rename renames oldRel to newRel. Both parents are traversed without
// following symlinks and both are fsynced after the rename, since the
// dirent change lands in each. A symlink at either final component is
// handled as a directory entry: the link itself is moved or replaced, never
// resolved.
func (r *Root) Rename(oldRel, newRel string) error {
	ofd, oldBase, oborrowed, err := r.openParent(oldRel)
	if err != nil {
		return err
	}
	if !oborrowed {
		defer unix.Close(ofd)
	}
	nfd, newBase, nborrowed, err := r.openParent(newRel)
	if err != nil {
		return err
	}
	if !nborrowed {
		defer unix.Close(nfd)
	}
	if err := unix.Renameat(ofd, oldBase, nfd, newBase); err != nil {
		return pathErr("renameat", oldRel+" -> "+newRel, err)
	}
	if err := unix.Fsync(ofd); err != nil {
		return pathErr("fsync", oldRel, err)
	}
	if err := unix.Fsync(nfd); err != nil {
		return pathErr("fsync", newRel, err)
	}
	return nil
}

// Remove unlinks the file at rel. It operates on the directory entry, so a
// final symlink is removed as a link and its target is never touched;
// directories are refused (unlink of a directory fails at the syscall).
// The parent directory is fsynced after a successful unlink.
func (r *Root) Remove(rel string) error {
	dirfd, base, borrowed, err := r.openParent(rel)
	if err != nil {
		return err
	}
	if !borrowed {
		defer unix.Close(dirfd)
	}
	if err := unix.Unlinkat(dirfd, base, 0); err != nil {
		return pathErr("unlinkat", rel, err)
	}
	if err := unix.Fsync(dirfd); err != nil {
		return pathErr("fsync", rel, err)
	}
	return nil
}

// SyncDir fsyncs the directory at rel so that earlier changes to its
// entries are durable. rel may be "", which names the anchor itself.
func (r *Root) SyncDir(rel string) error {
	var comps []string
	if rel != "" {
		var err error
		comps, err = validateRel(rel)
		if err != nil {
			return err
		}
	}
	fd, borrowed, err := r.openDir(comps)
	if err != nil {
		return err
	}
	if !borrowed {
		defer unix.Close(fd)
	}
	if err := unix.Fsync(fd); err != nil {
		return pathErr("fsync", rel, err)
	}
	return nil
}

// readAll reads fd to EOF with EINTR retry.
func readAll(fd int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64*1024)
	for {
		n, err := unix.Read(fd, buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return nil, err
		}
		if n == 0 {
			return out, nil
		}
	}
}

// writeAll writes all of data to fd with EINTR retry.
func writeAll(fd int, data []byte) error {
	for len(data) > 0 {
		n, err := unix.Write(fd, data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
