package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// DefaultLimits bound every capture unless the caller narrows them.
const (
	DefaultMaxEntries   int64 = 100000
	DefaultMaxFileBytes int64 = 1 << 30 // 1 GiB
	DefaultMaxTreeBytes int64 = 5 << 30 // 5 GiB
	blobPerm                  = 0o600
	storeDirPerm              = 0o700
	snapshotTempPrefix        = ".snapshot-tmp-"
)

// Limits bound one capture. Zero or negative fields fall back to the
// package defaults, so the zero Limits is the safest option, not the
// most permissive.
type Limits struct {
	MaxEntries   int64
	MaxFileBytes int64
	MaxTreeBytes int64
}

// withDefaults replaces non-positive fields with the package defaults.
func (l Limits) withDefaults() Limits {
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultMaxEntries
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxTreeBytes <= 0 {
		l.MaxTreeBytes = DefaultMaxTreeBytes
	}
	return l
}

// CaptureOptions parameterize a capture. Exclusions are root-relative
// patterns: an entry is excluded when its full slash-clean path matches a
// pattern via path.Match, or when any ancestor directory equals a pattern
// exactly — so "node_modules" drops the whole subtree while "*.log" drops
// only root-level logs (patterns are not recursive; exclude directories
// for subtrees). Digits and stars aside, patterns must themselves be
// valid, relative, slash-clean paths.
type CaptureOptions struct {
	Limits     Limits
	Exclusions []string
}

// Capture walks source (never following symlinks; every entry is lstat'd)
// and writes an immutable, content-addressed snapshot into store: file
// bytes and symlink target bytes become blobs under store/blobs/sha256,
// copied — never hardlinked — into the store. The returned Manifest lists
// every file, symlink, and directory (empty directories included) sorted
// by path. FIFOs, sockets, and devices fail closed with ErrSpecialFile;
// crossing a limit fails with ErrLimitExceeded.
//
// Capture is idempotent: identical trees produce identical manifests and
// blobs, and re-capturing over existing blobs rewrites the same content
// (temp file + atomic rename), so concurrent captures of the same content
// are safe.
func Capture(ctx context.Context, source, store string, opts CaptureOptions) (Manifest, error) {
	if err := validateAbsDir("source", source); err != nil {
		return Manifest{}, err
	}
	if err := validateAbsDir("store", store); err != nil {
		return Manifest{}, err
	}
	patterns, err := compileExclusions(opts.Exclusions)
	if err != nil {
		return Manifest{}, err
	}
	return capture(ctx, source, opts.Limits.withDefaults(), patterns, BlobDir(store))
}

// captureWalk carries one walk's state: the manifest under construction,
// the running tree-byte total, and the fixed parameters every entry is
// filtered and bounded against. It exists so the per-entry helpers take
// the walk instead of five loose arguments each.
type captureWalk struct {
	source    string
	limits    Limits
	patterns  []string
	blobDir   string
	manifest  Manifest
	treeBytes int64
}

// capture is the shared engine: blobDir == "" performs a digest-only walk
// (Verify) that writes nothing. The source root must be a real directory —
// lstat'd, never followed — so a symlink or file source is a typed
// ErrSourceNotDirectory instead of an empty manifest.
func capture(ctx context.Context, source string, limits Limits, patterns []string, blobDir string) (Manifest, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return Manifest{}, fmt.Errorf("snapshot: stat source %s: %w", source, err)
	}
	if !info.IsDir() {
		return Manifest{}, &SourceNotDirectoryError{Path: source, Got: fileKind(info)}
	}
	w := &captureWalk{
		source:   source,
		limits:   limits,
		patterns: patterns,
		blobDir:  blobDir,
		manifest: Manifest{SchemaVersion: SchemaVersion, Entries: []Entry{}},
	}
	if err := filepath.WalkDir(source, w.visit(ctx)); err != nil {
		return Manifest{}, err
	}
	return w.finish()
}

// visit is the walk callback: it wraps walk errors, checks cancellation,
// skips the implied root, and hands every other entry to admitEntry.
func (w *captureWalk) visit(ctx context.Context) fs.WalkDirFunc {
	return func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("snapshot: walk %s: %w", p, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == w.source {
			return nil // the root itself is implied
		}
		rel, err := filepath.Rel(w.source, p)
		if err != nil {
			return fmt.Errorf("snapshot: relativize %s: %w", p, err)
		}
		return w.admitEntry(p, filepath.ToSlash(rel), d)
	}
}

// admitEntry decides whether one walked path becomes a manifest entry:
// excluded paths are dropped (an excluded directory prunes its whole
// subtree) and the entry count is bounded; what survives is recorded.
func (w *captureWalk) admitEntry(p, rel string, d fs.DirEntry) error {
	if excluded(rel, w.patterns) {
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("snapshot: lstat %s: %w", p, err)
	}
	if int64(len(w.manifest.Entries))+1 > w.limits.MaxEntries {
		return &LimitError{What: "entries", Path: rel, Limit: w.limits.MaxEntries, Actual: int64(len(w.manifest.Entries)) + 1}
	}
	return w.recordEntry(p, rel, d, info)
}

// recordEntry appends the entry for one admitted path, dispatching on its
// kind; whatever cannot be recorded fails with a typed error.
func (w *captureWalk) recordEntry(p, rel string, d fs.DirEntry, info fs.FileInfo) error {
	switch {
	case info.Mode().IsRegular():
		return w.recordFile(p, rel, info)
	case info.Mode()&fs.ModeSymlink != 0:
		return w.recordSymlink(p, rel, info)
	case d.IsDir():
		w.manifest.Entries = append(w.manifest.Entries, Entry{Path: rel, Kind: KindDir, Mode: uint32(info.Mode().Perm())})
		return nil
	default:
		return &SpecialFileError{Path: rel, Kind: specialKind(info)}
	}
}

// recordFile bounds one regular file by the per-file and tree byte caps
// and stores its blob. The lstat size gates the caps up front; the
// streamed count is what settles the entry and the total, so a file
// growing mid-capture still fails closed.
func (w *captureWalk) recordFile(p, rel string, info fs.FileInfo) error {
	if info.Size() > w.limits.MaxFileBytes {
		return &LimitError{What: "file bytes", Path: rel, Limit: w.limits.MaxFileBytes, Actual: info.Size()}
	}
	if w.treeBytes+info.Size() > w.limits.MaxTreeBytes {
		return &LimitError{What: "tree bytes", Path: rel, Limit: w.limits.MaxTreeBytes, Actual: w.treeBytes + info.Size()}
	}
	digest, size, err := captureFile(p, rel, info.Size(), w.limits.MaxFileBytes, w.blobDir)
	if err != nil {
		return err
	}
	w.treeBytes += size
	w.manifest.Entries = append(w.manifest.Entries, Entry{
		Path: rel, Kind: KindFile, Mode: uint32(info.Mode().Perm()), Size: size, Digest: digest,
	})
	return nil
}

// recordSymlink reads the target and stores it as the symlink's content
// blob, never following it.
func (w *captureWalk) recordSymlink(p, rel string, info fs.FileInfo) error {
	target, err := os.Readlink(p)
	if err != nil {
		return fmt.Errorf("snapshot: readlink %s: %w", p, err)
	}
	digest, err := captureSymlink(rel, target, w.blobDir)
	if err != nil {
		return err
	}
	w.manifest.Entries = append(w.manifest.Entries, Entry{
		Path: rel, Kind: KindSymlink, Mode: uint32(info.Mode().Perm()),
		Size: int64(len(target)), Digest: digest, LinkTarget: target,
	})
	return nil
}

// specialKind names the kind of unrecordable file the walk must refuse.
func specialKind(info fs.FileInfo) string {
	switch {
	case info.Mode()&fs.ModeNamedPipe != 0:
		return "fifo"
	case info.Mode()&fs.ModeSocket != 0:
		return "socket"
	case info.Mode()&fs.ModeDevice != 0:
		return "device"
	}
	return "special file"
}

// finish sorts the entries, refuses duplicate paths, and seals the root
// digest; sorted order is what makes the manifest comparable.
func (w *captureWalk) finish() (Manifest, error) {
	m := w.manifest
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	for i := 1; i < len(m.Entries); i++ {
		if m.Entries[i-1].Path == m.Entries[i].Path {
			return Manifest{}, &DuplicatePathError{Path: m.Entries[i].Path}
		}
	}
	m.RootDigest = DigestManifest(m)
	return m, nil
}

// captureFile streams one regular file: digest under the blob domain and
// a store blob (temp file + rename, 0600, fsynced). The file is opened
// with O_NOFOLLOW (a symlink swapped in after lstat cannot redirect the
// read) and O_NONBLOCK (a swap to a FIFO cannot wedge the read — the
// fstat below still fails closed on non-regular content), and the byte
// count — not the lstat size — bounds the limits, so a file growing
// mid-capture still fails closed.
func captureFile(p, rel string, size, maxFile int64, blobDir string) (string, int64, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", 0, fmt.Errorf("snapshot: open %s: %w", p, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("snapshot: fstat %s: %w", p, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, &SpecialFileError{Path: rel, Kind: "swapped-for-special"}
	}
	digest, n, err := streamToBlob(f, maxFile, blobDir)
	if err != nil {
		return "", 0, err
	}
	return digest, n, nil
}

// captureSymlink stores the target string as the symlink's content blob.
func captureSymlink(rel, target, blobDir string) (string, error) {
	digest, _, err := streamToBlob(strings.NewReader(target), int64(len(target)), blobDir)
	if err != nil {
		return "", fmt.Errorf("snapshot: store symlink %s blob: %w", rel, err)
	}
	return digest, nil
}

// blobHasher returns a SHA-256 hasher pre-fed with the blob domain
// prefix, so blob digests are domain-separated from every other digest
// use.
func blobHasher() hash.Hash {
	h := sha256.New()
	fmt.Fprintf(h, "%s:", domainBlob)
	return h
}

// digestStream digests r under the blob domain.
func digestStream(r io.Reader) (string, error) {
	h := blobHasher()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("snapshot: read content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// digestBytes digests data under the blob domain.
func digestBytes(data []byte) string {
	h := blobHasher()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// DigestBlob returns the content digest of data under the blob domain —
// the digest that names store blobs and manifest entry content. Use this
// (not fingerprint.Bytes with the domain constant, which would
// double-prefix) whenever content digests are composed by hand.
func DigestBlob(data []byte) string { return digestBytes(data) }

// streamToBlob digests r under the blob domain while copying it to a
// store blob. When blobDir is "" nothing is written (digest-only walks).
// The copy is capped at max bytes; a longer stream fails closed.
func streamToBlob(r io.Reader, max int64, blobDir string) (string, int64, error) {
	h := blobHasher()
	capped := io.LimitReader(r, max+1)
	var sink io.Writer = h
	var tmp *os.File
	defer func() {
		if tmp != nil {
			tmp.Close()
			if tmp.Name() != "" {
				os.Remove(tmp.Name())
			}
		}
	}()
	if blobDir != "" {
		if err := os.MkdirAll(blobDir, storeDirPerm); err != nil {
			return "", 0, fmt.Errorf("snapshot: mkdir blob store: %w", err)
		}
		var err error
		tmp, err = os.CreateTemp(blobDir, snapshotTempPrefix)
		if err != nil {
			return "", 0, fmt.Errorf("snapshot: create blob temp: %w", err)
		}
		if err := tmp.Chmod(blobPerm); err != nil {
			return "", 0, fmt.Errorf("snapshot: chmod blob temp: %w", err)
		}
		sink = io.MultiWriter(h, tmp)
	}
	n, err := io.Copy(sink, capped)
	if err != nil {
		return "", 0, fmt.Errorf("snapshot: read content: %w", err)
	}
	if n > max {
		return "", 0, &LimitError{What: "file bytes", Limit: max, Actual: n}
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if blobDir == "" {
		return digest, n, nil
	}
	final := filepath.Join(blobDir, digest)
	if info, err := os.Lstat(final); err == nil && info.Mode().IsRegular() && info.Size() == n {
		// Idempotent: a blob of this digest is already stored — but its
		// name alone is not trusted. Bytes that no longer hash to the
		// digest (same-length corruption) fail closed here instead of
		// poisoning every later materialize.
		if err := verifyBlobFile(final, digest); err != nil {
			return "", 0, err
		}
		return digest, n, nil
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("snapshot: fsync blob temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("snapshot: close blob temp: %w", err)
	}
	name := tmp.Name()
	tmp = nil // ownership moved to the rename
	if err := os.Rename(name, final); err != nil {
		os.Remove(name)
		return "", 0, fmt.Errorf("snapshot: install blob: %w", err)
	}
	if dir, err := os.Open(blobDir); err == nil {
		dir.Sync()
		dir.Close()
	}
	return digest, n, nil
}

// verifyBlobFile reads the blob at path (O_NOFOLLOW|O_NONBLOCK — the
// store is machine-owned but never trusted blindly) and requires its
// bytes to hash to digest under the blob domain.
func verifyBlobFile(path, digest string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return &BlobMissingError{Digest: digest}
	}
	defer f.Close()
	got, err := digestStream(f)
	if err != nil {
		return fmt.Errorf("snapshot: read blob %s: %w", digest, err)
	}
	if got != digest {
		return &BlobCorruptError{Digest: digest}
	}
	return nil
}

// compileExclusions validates each pattern as a relative slash-clean path
// with valid path.Match syntax.
func compileExclusions(patterns []string) ([]string, error) {
	out := make([]string, 0, len(patterns))
	for _, pat := range patterns {
		if err := ValidateRelPath(pat); err != nil {
			return nil, &InvalidExclusionError{Pattern: pat, Reason: "not a relative slash-clean path"}
		}
		if _, err := path.Match(pat, ""); err != nil {
			return nil, &InvalidExclusionError{Pattern: pat, Reason: "invalid glob syntax"}
		}
		out = append(out, pat)
	}
	return out, nil
}

// excluded reports whether rel is covered by the compiled patterns: a
// full match, or an ancestor directory equal to a pattern.
func excluded(rel string, patterns []string) bool {
	for _, pat := range patterns {
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		if anc := path.Dir(rel); anc != "." && anc != "/" {
			for p := anc; ; {
				if p == pat {
					return true
				}
				if p == "." || p == "/" {
					break
				}
				p = path.Dir(p)
			}
		}
	}
	return false
}

// validateAbsDir checks that dir is an absolute, clean, backslash-free
// path (mirroring gitx's repository-dir rule).
func validateAbsDir(what, dir string) error {
	if dir == "" {
		return fmt.Errorf("snapshot: %s directory must not be empty", what)
	}
	if strings.ContainsRune(dir, '\\') {
		return fmt.Errorf("snapshot: %s directory %q must use '/' as its only separator", what, dir)
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("snapshot: %s directory %q must be absolute", what, dir)
	}
	if filepath.Clean(dir) != dir {
		return fmt.Errorf("snapshot: %s directory %q must be clean", what, dir)
	}
	return nil
}
