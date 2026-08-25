//go:build linux || darwin

package securefs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"

	"golang.org/x/sys/unix"
)

// WriteAtomic atomically replaces the file at rel with data. The content is
// written to a uniquely named temporary file in the destination directory,
// fsynced, and renamed over the destination, and the directory is then
// fsynced — after a crash rel holds either its old or its new content,
// never a partial file, and no temp file survives.
//
// mode is the permission for a newly created file (subject to no umask: the
// exact perm is set with fchmod). An existing regular destination keeps its
// on-disk permission bits; special bits (setuid/setgid/sticky) are not
// carried. A destination that exists and is not a regular file — a
// directory, fifo, device, or symlink — is refused, and a failed write
// leaves the original bytes intact.
func (r *Root) WriteAtomic(rel string, data []byte, mode fs.FileMode) error {
	dirfd, base, borrowed, err := r.openParent(rel)
	if err != nil {
		return err
	}
	if !borrowed {
		defer unix.Close(dirfd)
	}

	fileMode, err := destMode(dirfd, base, rel, mode)
	if err != nil {
		return err
	}
	tmpName, tmpFd, err := createTemp(dirfd, fileMode)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			if tmpFd >= 0 {
				unix.Close(tmpFd)
			}
			if rmErr := unix.Unlinkat(dirfd, tmpName, 0); rmErr == nil {
				_ = unix.Fsync(dirfd)
			}
		}
	}()

	if err := unix.Fchmod(tmpFd, uint32(fileMode)); err != nil {
		return pathErr("fchmod", rel, err)
	}
	if err := writeAll(tmpFd, data); err != nil {
		return pathErr("write", rel, err)
	}
	if err := unix.Fsync(tmpFd); err != nil {
		return pathErr("fsync", rel, err)
	}
	// close() releases the fd even when it reports an error, so the
	// sentinel must be cleared before checking: the deferred cleanup must
	// never close the fd twice (a reused fd number could close an
	// unrelated descriptor).
	err = unix.Close(tmpFd)
	tmpFd = -1
	if err != nil {
		return pathErr("close", rel, err)
	}
	if err := unix.Renameat(dirfd, tmpName, dirfd, base); err != nil {
		return pathErr("renameat", rel, err)
	}
	renamed = true
	if err := unix.Fsync(dirfd); err != nil {
		return pathErr("fsync", rel, err)
	}
	return nil
}

// CreateExclusive creates the file at rel with data in one claim: the
// open uses O_CREAT|O_EXCL|O_NOFOLLOW, so the call fails if anything —
// including a symlink — already exists at rel, and never resolves one. The
// exact perm is set with fchmod regardless of umask. The file and its
// parent directory are fsynced before the call returns.
//
// Creation is atomic in CONTENT as well as in existence: the destination
// never exists in a partially written state, so a concurrent reader sees
// either nothing or the whole file. A failure at any point before the link
// leaves no trace; a failure after it keeps the completed object, because
// unlinking a file-fsynced object over a directory-durability error would
// discard confirmed-durable bytes.
func (r *Root) CreateExclusive(rel string, data []byte, mode fs.FileMode) error {
	dirfd, base, borrowed, err := r.openParent(rel)
	if err != nil {
		return err
	}
	if !borrowed {
		defer unix.Close(dirfd)
	}

	// The content is written to a temporary file FIRST and linked into
	// place afterwards. Creating the destination with O_EXCL and then
	// writing it would be exclusive but not atomic: a concurrent reader
	// that opens the winner's file between the create and the write sees
	// an EMPTY file, and a reader that concludes "not registered" from
	// that is a reader about to claim something already taken.
	//
	// linkat provides the exclusivity the create used to: it fails with
	// EEXIST when the destination is taken, and the file it links is
	// already complete and fsynced.
	name, fd, err := createTemp(dirfd, mode)
	if err != nil {
		return err
	}
	fdOpen, linked := true, false
	defer func() {
		if fdOpen {
			unix.Close(fd)
		}
		if !linked {
			if rmErr := unix.Unlinkat(dirfd, name, 0); rmErr == nil {
				_ = unix.Fsync(dirfd)
			}
		}
	}()

	if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
		return pathErr("fchmod", rel, err)
	}
	if err := writeAll(fd, data); err != nil {
		return pathErr("write", rel, err)
	}
	if err := unix.Fsync(fd); err != nil {
		return pathErr("fsync", rel, err)
	}
	if err := unix.Close(fd); err != nil {
		fdOpen = false
		return pathErr("close", rel, err)
	}
	fdOpen = false

	if err := unix.Linkat(dirfd, name, dirfd, base, 0); err != nil {
		return pathErr("linkat", rel, err)
	}
	linked = true
	// The temporary name is now a second link to the same complete
	// object; unlinking it leaves the destination intact.
	if rmErr := unix.Unlinkat(dirfd, name, 0); rmErr != nil {
		return pathErr("unlink", name, rmErr)
	}
	if err := unix.Fsync(dirfd); err != nil {
		return pathErr("fsync", rel, err)
	}
	return nil
}

// destMode returns the perm a WriteAtomic temp must carry: the on-disk
// permission bits of an existing regular destination, or the caller's mode
// for a new file. Anything else at the destination — including a symlink,
// which lstat-style Fstatat reports without resolving — fails closed.
func destMode(dirfd int, base, rel string, mode fs.FileMode) (fs.FileMode, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return mode.Perm(), nil
		}
		return 0, pathErr("fstatat", rel, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, fmt.Errorf("securefs: write %s: not a regular file", rel)
	}
	return fs.FileMode(st.Mode & 0o777), nil
}

// createTemp creates a uniquely named temporary file under dirfd with
// O_CREAT|O_EXCL|O_NOFOLLOW and a crypto/random suffix, retrying on
// collision the way os.CreateTemp does.
func createTemp(dirfd int, mode fs.FileMode) (string, int, error) {
	var rnd [8]byte
	for i := 0; i < 10000; i++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			return "", -1, fmt.Errorf("securefs: read random bytes: %w", err)
		}
		name := tmpPrefix + hex.EncodeToString(rnd[:])
		fd, err := unix.Openat(dirfd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
		if err == nil {
			return name, fd, nil
		}
		if err == unix.EEXIST {
			continue
		}
		return "", -1, pathErr("openat", name, err)
	}
	return "", -1, fmt.Errorf("securefs: too many temporary file collisions under %s", tmpPrefix)
}
