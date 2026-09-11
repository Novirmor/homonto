package workspacemigration

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
)

// migrationFSOps is deliberately limited to the operations whose ordering is
// part of migration durability. Tests replace individual functions to prove
// that a failed directory sync never advances a journal phase.
type migrationFSOps struct {
	mkdir    func(*os.Root, string, os.FileMode) error
	openFile func(*os.Root, string, int, os.FileMode) (*os.File, error)
	symlink  func(*os.Root, string, string) error
	rename   func(*os.Root, string, string) error
	remove   func(*os.Root, string) error
	sync     func(string, string, *os.File) error
}

var defaultMigrationFSOps = migrationFSOps{
	mkdir: func(root *os.Root, name string, mode os.FileMode) error { return root.Mkdir(name, mode) },
	openFile: func(root *os.Root, name string, flag int, mode os.FileMode) (*os.File, error) {
		return root.OpenFile(name, flag, mode)
	},
	symlink: func(root *os.Root, target, name string) error { return root.Symlink(target, name) },
	rename:  func(root *os.Root, oldName, newName string) error { return root.Rename(oldName, newName) },
	remove:  func(root *os.Root, name string) error { return root.Remove(name) },
	sync:    func(_ string, _ string, file *os.File) error { return file.Sync() },
}

var migrationFS = defaultMigrationFSOps

const migrationTempPrefix = ".homonto-migration-"

// migrationAfterParentPin is a deterministic test seam for the interval after
// a target's real parent is descriptor-pinned and before any target mutation.
// Production leaves it as a no-op.
var migrationAfterParentPin = func(string, string) error { return nil }

// migrationAfterTemporaryCreate is a deterministic test seam for the interval
// after a temporary file is created at its private creation mode and before it
// is chmodded or populated. Production leaves it as a no-op.
var migrationAfterTemporaryCreate = func(string) error { return nil }

// migrationRegularExpectation is the pre/post image already classified by the
// journal executor. It is passed back into the descriptor-pinned mutation so a
// same-inode, in-place edit cannot be overwritten merely because its pathname
// and inode still match the first classification.
type migrationRegularExpectation struct {
	exists bool
	data   []byte
	mode   os.FileMode
}

// migrationTempAuthority is private journal authority for exactly one atomic
// replacement image. The plan declares the destination and generation rule;
// this concrete binding adds the run owner, both journaled images, and the
// selected direction before a temporary pathname is created.
type migrationTempAuthority struct {
	root      string
	runID     string
	owner     string
	scope     string
	kind      string
	target    string
	direction string

	preExists  bool
	preimage   []byte
	preMode    os.FileMode
	postExists bool
	postimage  []byte
	postMode   os.FileMode

	data        []byte
	mode        os.FileMode
	privateSlot bool
	bootstrap   bool

	recoveryDescriptor string
	syncKind           string
}

func (authority migrationTempAuthority) name() (string, error) {
	if !migrationrecord.SafeRunID(authority.runID) || authority.scope == "" || authority.kind == "" || authority.direction == "" || !filepath.IsAbs(authority.root) || filepath.Clean(authority.root) != authority.root || !filepath.IsAbs(authority.target) || filepath.Clean(authority.target) != authority.target || !pathWithin(authority.root, authority.target) || authority.mode.Perm() == 0 {
		return "", fmt.Errorf("workspace migration: invalid temporary-file authority")
	}
	if authority.bootstrap {
		if authority.scope != journalScopeRecovery || authority.kind != journalKindRecoveryStatus || authority.target != filepath.Join(authority.root, ".workflow", "migrations", authority.runID, "private", "journal.json") {
			return "", fmt.Errorf("workspace migration: invalid bootstrap temporary-file authority")
		}
		return migrationTempPrefix + migrationTempToken("preparation-status-bootstrap", authority.runID, authority.target, authority.direction), nil
	}
	if !validMigrationOwner(authority.owner) {
		return "", fmt.Errorf("workspace migration: invalid temporary-file authority")
	}
	if authority.privateSlot {
		if !validDigest(authority.recoveryDescriptor) {
			return "", fmt.Errorf("workspace migration: invalid private recovery payload authority")
		}
		return migrationTempPrefix + migrationTempToken("private-payload-v3", authority.runID, authority.owner, authority.scope, authority.kind, authority.target, authority.direction, authority.recoveryDescriptor), nil
	}
	return migrationTempPrefix + migrationTempToken(
		"exact-image",
		authority.runID,
		authority.owner,
		authority.scope,
		authority.kind,
		authority.target,
		authority.direction,
		strconv.FormatBool(authority.preExists),
		migrationDigest(authority.preimage),
		strconv.FormatUint(uint64(authority.preMode.Perm()), 8),
		strconv.FormatBool(authority.postExists),
		migrationDigest(authority.postimage),
		strconv.FormatUint(uint64(authority.postMode.Perm()), 8),
		migrationDigest(authority.data),
		strconv.FormatUint(uint64(authority.mode.Perm()), 8),
	), nil
}

func migrationTempToken(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func (authority migrationTempAuthority) matchesWrite(path string, data []byte, mode os.FileMode) bool {
	return authority.target == path && bytes.Equal(authority.data, data) && authority.mode.Perm() == mode.Perm()
}

func (authority migrationTempAuthority) matchesPartial(data []byte, mode os.FileMode) bool {
	if authority.bootstrap {
		// The bootstrap name does not include an owner or image digest. Until a
		// full canonical status image proves the run identity, partial bytes
		// cannot authorize cleanup.
		return bytes.Equal(data, authority.data) && mode.Perm() == authority.mode.Perm()
	}
	if authority.privateSlot && authority.data == nil {
		// A durable run/owner identity names the slot, but does not authenticate
		// arbitrary bytes inside it. Cleanup callers without the expected payload
		// must preserve the artifact for operator review rather than treating a
		// private 0600 mode as sufficient proof of ownership.
		return false
	}
	// An authority with expected replacement bytes may reclaim only the empty
	// 0600 creation image or a prefix at the expected mode.
	if len(data) == 0 && mode.Perm() == 0o600 {
		return true
	}
	if len(data) > len(authority.data) || !bytes.Equal(data, authority.data[:len(data)]) {
		return false
	}
	return mode.Perm() == authority.mode.Perm()
}

// requireMigrationFilesystemSupport refuses platforms where the guarantees this
// transaction relies on are unavailable before it creates an intent, backup, or
// authoritative record. os.Root tracks open directories across renames on the
// supported Unix targets below; Go documents weaker rename/TOCTOU semantics on
// js and plan9. Windows currently has no kernel-released guardian lock in
// applylock, so accepting a killed migration there would falsely promise R7
// recovery.
func requireMigrationFilesystemSupport() error {
	switch runtime.GOOS {
	case "darwin", "dragonfly", "freebsd", "linux", "netbsd", "openbsd":
		return nil
	default:
		return fmt.Errorf("workspace migration: descriptor-pinned recovery is unsupported on %s", runtime.GOOS)
	}
}

func migrationRelativePath(root, path string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("workspace migration: migration path must be canonical and absolute")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("workspace migration: path outside owned root")
	}
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." || component == ".." {
			return "", fmt.Errorf("workspace migration: unsafe owned path")
		}
	}
	return rel, nil
}

func migrationRealDirectory(info os.FileInfo) bool {
	return info != nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func migrationRealRegular(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

// openPinnedMigrationRoot verifies the pathname before and after OpenRoot.
// OpenRoot follows a symlink at its initial name, so accepting only its handle
// would otherwise let a parent replacement become the approved root.
func openPinnedMigrationRoot(root string) (*os.Root, error) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("workspace migration: owned root must be canonical and absolute")
	}
	volumeRoot := filepath.VolumeName(root) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(volumeRoot, root); err != nil {
		return nil, err
	}
	before, err := os.Lstat(root)
	if err != nil || !migrationRealDirectory(before) {
		return nil, fmt.Errorf("workspace migration: owned root is not a real directory")
	}
	pinned, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	opened, statErr := pinned.Stat(".")
	after, afterErr := os.Lstat(root)
	if statErr != nil || afterErr != nil || !migrationRealDirectory(after) || !migrationRealDirectory(opened) || !os.SameFile(before, opened) || !os.SameFile(before, after) {
		_ = pinned.Close()
		return nil, fmt.Errorf("workspace migration: owned root changed while being pinned")
	}
	return pinned, nil
}

func openPinnedMigrationChild(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	if !migrationRealDirectory(expected) {
		return nil, fmt.Errorf("workspace migration: parent component is not a real directory")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := child.Stat(".")
	after, afterErr := parent.Lstat(name)
	if statErr != nil || afterErr != nil || !migrationRealDirectory(after) || !migrationRealDirectory(opened) || !os.SameFile(expected, opened) || !os.SameFile(expected, after) {
		_ = child.Close()
		return nil, fmt.Errorf("workspace migration: parent directory changed while being pinned")
	}
	return child, nil
}

// ensureMigrationDirectory creates each missing component one at a time and
// syncs the containing directory before proceeding. A crash can therefore
// leave either an absent component or a durable, real directory, never an
// unsynced ancestor that later journal progress assumes exists.
func ensureMigrationDirectory(root, dir string, mode os.FileMode) error {
	if err := requireMigrationFilesystemSupport(); err != nil {
		return err
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("workspace migration: migration directory must be canonical and absolute")
	}
	if filepath.Clean(root) == filepath.Clean(dir) {
		pinned, err := openPinnedMigrationRoot(root)
		if err != nil {
			return err
		}
		return pinned.Close()
	}
	rel, err := migrationRelativePath(root, dir)
	if err != nil {
		return err
	}
	current, err := openPinnedMigrationRoot(root)
	if err != nil {
		return err
	}
	currentPath := root
	defer func() { _ = current.Close() }()
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		info, statErr := current.Lstat(component)
		created := false
		if errors.Is(statErr, os.ErrNotExist) {
			mkdirErr := migrationFS.mkdir(current, component, mode)
			if mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return mkdirErr
			}
			info, statErr = current.Lstat(component)
			if statErr != nil {
				return statErr
			}
			created = mkdirErr == nil
			if created {
				if err := syncPinnedMigrationDirectory(current, currentPath); err != nil {
					return err
				}
			}
		} else if statErr != nil {
			return statErr
		}
		child, err := openPinnedMigrationChild(current, component, info)
		if err != nil {
			return err
		}
		if created {
			dirFile, err := child.Open(".")
			if err != nil {
				_ = child.Close()
				return err
			}
			chmodErr := dirFile.Chmod(mode)
			syncErr := migrationFS.sync("directory-mode", filepath.Join(currentPath, component), dirFile)
			closeErr := dirFile.Close()
			if err := errors.Join(chmodErr, syncErr, closeErr); err != nil {
				_ = child.Close()
				return err
			}
		}
		if err := current.Close(); err != nil {
			_ = child.Close()
			return err
		}
		current = child
		currentPath = filepath.Join(currentPath, component)
	}
	return nil
}

// openPinnedMigrationParent returns a handle to the target parent and keeps it
// open through classification and mutation. All methods below operate on the
// basename through that handle, not through a pathname that an attacker can
// replace after the check.
func openPinnedMigrationParent(root, path string) (*os.Root, string, string, error) {
	rel, err := migrationRelativePath(root, path)
	if err != nil {
		return nil, "", "", err
	}
	parentRel, name := filepath.Dir(rel), filepath.Base(rel)
	current, err := openPinnedMigrationRoot(root)
	if err != nil {
		return nil, "", "", err
	}
	if parentRel == "." {
		return current, name, root, nil
	}
	parentPath := root
	for _, component := range strings.Split(parentRel, string(os.PathSeparator)) {
		info, err := current.Lstat(component)
		if err != nil {
			_ = current.Close()
			return nil, "", "", err
		}
		child, err := openPinnedMigrationChild(current, component, info)
		if err != nil {
			_ = current.Close()
			return nil, "", "", err
		}
		if err := current.Close(); err != nil {
			_ = child.Close()
			return nil, "", "", err
		}
		current = child
		parentPath = filepath.Join(parentPath, component)
	}
	return current, name, parentPath, nil
}

// migrationPinnedParentStillCurrent catches a deterministic replacement after
// a parent was opened but before its descriptor is used for a mutation. The
// descriptor remains safe in that case, but writing through it would affect an
// unlinked directory instead of the reviewed pathname.
func migrationPinnedParentStillCurrent(root, parentPath string, pinned *os.Root) error {
	expected, err := pinned.Stat(".")
	if err != nil || !migrationRealDirectory(expected) {
		return fmt.Errorf("workspace migration: pinned parent is no longer a real directory")
	}
	current, err := openPinnedMigrationRoot(root)
	if err != nil {
		return err
	}
	defer current.Close()
	if filepath.Clean(parentPath) != filepath.Clean(root) {
		rel, err := migrationRelativePath(root, parentPath)
		if err != nil {
			return err
		}
		for _, component := range strings.Split(rel, string(os.PathSeparator)) {
			info, err := current.Lstat(component)
			if err != nil {
				return err
			}
			child, err := openPinnedMigrationChild(current, component, info)
			if err != nil {
				return err
			}
			if err := current.Close(); err != nil {
				_ = child.Close()
				return err
			}
			current = child
		}
	}
	actual, err := current.Stat(".")
	if err != nil || !migrationRealDirectory(actual) || !os.SameFile(expected, actual) {
		return fmt.Errorf("workspace migration: parent directory changed while being pinned")
	}
	return nil
}

func syncPinnedMigrationDirectory(parent *os.Root, path string) error {
	return syncPinnedMigrationDirectoryKind(parent, path, "directory")
}

func syncPinnedMigrationDirectoryKind(parent *os.Root, path, kind string) error {
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	syncErr := migrationFS.sync(kind, path, dir)
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func writeMigrationFile(file *os.File, data []byte) error {
	for len(data) != 0 {
		n, err := file.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func migrationTempFile(parent *os.Root) (*os.File, string, error) {
	for range 32 {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, "", err
		}
		name := migrationTempPrefix + hex.EncodeToString(token[:])
		file, err := migrationFS.openFile(parent, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", fmt.Errorf("workspace migration: cannot allocate a unique temporary file")
}

func migrationAuthorizedTempFile(parent *os.Root, authority migrationTempAuthority) (*os.File, string, error) {
	name, err := authority.name()
	if err != nil {
		return nil, "", err
	}
	data, mode, exists, err := migrationPinnedOptionalRegular(parent, name)
	if err != nil {
		return nil, "", err
	}
	if exists {
		if !authority.matchesPartial(data, mode) {
			return nil, "", fmt.Errorf("workspace migration: foreign or altered temporary file %q", name)
		}
		if err := migrationFS.remove(parent, name); err != nil {
			return nil, "", err
		}
		if err := syncPinnedMigrationDirectory(parent, filepath.Dir(authority.target)); err != nil {
			return nil, "", err
		}
	}
	file, err := migrationFS.openFile(parent, name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	return file, name, nil
}

func removeMigrationAuthorizedTemp(authority migrationTempAuthority) error {
	name, err := authority.name()
	if err != nil {
		return err
	}
	parent, _, parentPath, err := openPinnedMigrationParent(authority.root, authority.target)
	if err != nil {
		return err
	}
	defer parent.Close()
	data, mode, exists, err := migrationPinnedOptionalRegular(parent, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if !authority.matchesPartial(data, mode) {
		return fmt.Errorf("workspace migration: foreign or altered temporary file %q", name)
	}
	if err := migrationFS.remove(parent, name); err != nil {
		return err
	}
	return syncPinnedMigrationDirectory(parent, parentPath)
}

func migrationTargetStillExpected(parent *os.Root, name string, expected os.FileInfo, exists bool) error {
	info, err := parent.Lstat(name)
	if !exists {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace migration: target appeared after classification")
	}
	if err != nil || !migrationRealRegular(info) || !os.SameFile(expected, info) {
		return fmt.Errorf("workspace migration: target changed after classification")
	}
	return nil
}

// migrationPinnedOptionalRegular reads a target through its already-pinned
// parent and checks that the name still names the same regular file throughout
// the read. It intentionally gives callers one final content check immediately
// before their mutation. POSIX does not provide a byte-level compare-and-swap
// against an editor that writes after that check, so the migration requires the
// documented writer quiescence; it does detect edits present at this final
// descriptor-pinned check and keeps the directory capability pinned.
func migrationPinnedOptionalRegular(parent *os.Root, name string) ([]byte, os.FileMode, bool, error) {
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !migrationRealRegular(before) {
		return nil, 0, false, fmt.Errorf("workspace migration: expected a real regular file")
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, 0, false, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !migrationRealRegular(opened) || !os.SameFile(before, opened) || opened.Mode().Perm() != before.Mode().Perm() {
		_ = file.Close()
		return nil, 0, false, fmt.Errorf("workspace migration: file changed while being read")
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, 0, false, err
	}
	after, err := parent.Lstat(name)
	if err != nil || !migrationRealRegular(after) || !os.SameFile(before, after) || after.Mode().Perm() != before.Mode().Perm() {
		return nil, 0, false, fmt.Errorf("workspace migration: target changed while being read")
	}
	return data, before.Mode().Perm(), true, nil
}

func migrationTargetMatchesExpectation(parent *os.Root, name, path string, expected *migrationRegularExpectation) error {
	if expected == nil {
		return nil
	}
	data, mode, exists, err := migrationPinnedOptionalRegular(parent, name)
	if err != nil || exists != expected.exists || exists && (!bytes.Equal(data, expected.data) || mode.Perm() != expected.mode.Perm()) {
		return fmt.Errorf("workspace migration: recovery conflict at %s", path)
	}
	return nil
}

func migrationDirectoryStillExpected(parent *os.Root, name string, expected os.FileInfo) error {
	info, err := parent.Lstat(name)
	if err != nil || !migrationRealDirectory(info) || !os.SameFile(expected, info) {
		return fmt.Errorf("workspace migration: directory changed after classification")
	}
	return nil
}

// writeMigrationRegular atomically replaces a regular migration-owned file.
// It preserves an existing mode, writes and syncs the temp through its file
// descriptor, renames through the pinned parent, then syncs that same parent.
func writeMigrationRegular(root, path string, data []byte, mode os.FileMode) error {
	return writeMigrationRegularExpected(root, path, data, mode, nil)
}

func writeMigrationRegularExpected(root, path string, data []byte, mode os.FileMode, expected *migrationRegularExpectation) error {
	return writeMigrationRegularWithAuthority(root, path, data, mode, expected, nil)
}

func writeMigrationRegularAuthorized(root, path string, data []byte, mode os.FileMode, expected *migrationRegularExpectation, authority migrationTempAuthority) error {
	return writeMigrationRegularWithAuthority(root, path, data, mode, expected, &authority)
}

func writeMigrationRegularWithAuthority(root, path string, data []byte, mode os.FileMode, expected *migrationRegularExpectation, authority *migrationTempAuthority) error {
	if err := ensureMigrationDirectory(root, filepath.Dir(path), 0o755); err != nil {
		return err
	}
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	existing, err := parent.Lstat(name)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists && !migrationRealRegular(existing) {
		return fmt.Errorf("workspace migration: refusing to write a non-regular migration file")
	}
	if exists {
		mode = existing.Mode().Perm()
	}
	if authority != nil {
		if !authority.matchesWrite(path, data, mode) {
			return fmt.Errorf("workspace migration: temporary-file authority does not match write target")
		}
		if _, err := authority.name(); err != nil {
			return err
		}
	}
	if err := migrationAfterParentPin(root, path); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	var temp *os.File
	var tempName string
	if authority == nil {
		temp, tempName, err = migrationTempFile(parent)
	} else {
		temp, tempName, err = migrationAuthorizedTempFile(parent, *authority)
	}
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = temp.Close()
			_ = migrationFS.remove(parent, tempName)
		}
	}()
	if err := migrationAfterTemporaryCreate(path); err != nil {
		return err
	}
	chmodErr := temp.Chmod(mode)
	writeErr := error(nil)
	if chmodErr == nil {
		writeErr = writeMigrationFile(temp, data)
	}
	syncErr := error(nil)
	if chmodErr == nil && writeErr == nil {
		syncKind := "file"
		if authority != nil && authority.syncKind != "" {
			syncKind = authority.syncKind
		}
		syncErr = migrationFS.sync(syncKind, path, temp)
	}
	closeErr := temp.Close()
	if err := errors.Join(chmodErr, writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	if err := migrationTargetStillExpected(parent, name, existing, exists); err != nil {
		return err
	}
	if err := migrationTargetMatchesExpectation(parent, name, path, expected); err != nil {
		return err
	}
	if err := migrationFS.rename(parent, tempName, name); err != nil {
		return err
	}
	renamed = true
	return syncPinnedMigrationDirectory(parent, parentPath)
}

func readMigrationOptionalRegular(root, path string) ([]byte, os.FileMode, bool, error) {
	parent, name, _, err := openPinnedMigrationParent(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !migrationRealRegular(before) {
		return nil, 0, false, fmt.Errorf("workspace migration: expected a real regular file")
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, 0, false, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !migrationRealRegular(opened) || !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, 0, false, fmt.Errorf("workspace migration: file changed while being read")
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, 0, false, err
	}
	return data, before.Mode().Perm(), true, nil
}

func readMigrationRegular(root, path string) ([]byte, error) {
	data, _, exists, err := readMigrationOptionalRegular(root, path)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func removeMigrationOptionalRegular(root, path string) error {
	return removeMigrationOptionalRegularExpected(root, path, nil)
}

func removeMigrationOptionalRegularExpected(root, path string, expected *migrationRegularExpectation) error {
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if errors.Is(err, os.ErrNotExist) {
		if expected != nil && expected.exists {
			return fmt.Errorf("workspace migration: recovery conflict at %s", path)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		if expected != nil && expected.exists {
			return fmt.Errorf("workspace migration: recovery conflict at %s", path)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if !migrationRealRegular(before) {
		return fmt.Errorf("workspace migration: refusing to remove a non-regular file")
	}
	if err := migrationAfterParentPin(root, path); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	if err := migrationTargetStillExpected(parent, name, before, true); err != nil {
		return err
	}
	if err := migrationTargetMatchesExpectation(parent, name, path, expected); err != nil {
		return err
	}
	if err := migrationFS.remove(parent, name); err != nil {
		return err
	}
	return syncPinnedMigrationDirectory(parent, parentPath)
}

func migrationTemporaryFileName(name string) bool {
	token := strings.TrimPrefix(name, migrationTempPrefix)
	if token == name || len(token) != 32 || token != strings.ToLower(token) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
}

func removeMigrationEmptyDirectory(root, path string) error {
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !migrationRealDirectory(before) {
		return fmt.Errorf("workspace migration: refusing to remove a non-directory")
	}
	dir, err := parent.Open(name)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("workspace migration: refusing to remove a non-empty directory")
	}
	if err := migrationAfterParentPin(root, path); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	if err := migrationDirectoryStillExpected(parent, name, before); err != nil {
		return err
	}
	if err := migrationFS.remove(parent, name); err != nil {
		return err
	}
	return syncPinnedMigrationDirectory(parent, parentPath)
}
