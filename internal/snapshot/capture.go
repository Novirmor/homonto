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

// capture is the shared engine: blobDir == "" performs a digest-only walk
// (Verify) that writes nothing.
func capture(ctx context.Context, source string, limits Limits, patterns []string, blobDir string) (Manifest, error) {
	m := Manifest{SchemaVersion: SchemaVersion, Entries: []Entry{}}
	var treeBytes int64

	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("snapshot: walk %s: %w", p, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == source {
			return nil // the root itself is implied
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return fmt.Errorf("snapshot: relativize %s: %w", p, err)
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, patterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("snapshot: lstat %s: %w", p, err)
		}
		if int64(len(m.Entries))+1 > limits.MaxEntries {
			return &LimitError{What: "entries", Path: rel, Limit: limits.MaxEntries, Actual: int64(len(m.Entries)) + 1}
		}

		switch {
		case info.Mode().IsRegular():
			if info.Size() > limits.MaxFileBytes {
				return &LimitError{What: "file bytes", Path: rel, Limit: limits.MaxFileBytes, Actual: info.Size()}
			}
			if treeBytes+info.Size() > limits.MaxTreeBytes {
				return &LimitError{What: "tree bytes", Path: rel, Limit: limits.MaxTreeBytes, Actual: treeBytes + info.Size()}
			}
			digest, size, err := captureFile(p, rel, info.Size(), limits.MaxFileBytes, blobDir)
			if err != nil {
				return err
			}
			treeBytes += size
			m.Entries = append(m.Entries, Entry{
				Path: rel, Kind: KindFile, Mode: uint32(info.Mode().Perm()), Size: size, Digest: digest,
			})
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return fmt.Errorf("snapshot: readlink %s: %w", p, err)
			}
			digest, err := captureSymlink(rel, target, blobDir)
			if err != nil {
				return err
			}
			m.Entries = append(m.Entries, Entry{
				Path: rel, Kind: KindSymlink, Mode: uint32(info.Mode().Perm()),
				Size: int64(len(target)), Digest: digest, LinkTarget: target,
			})
		case d.IsDir():
			m.Entries = append(m.Entries, Entry{Path: rel, Kind: KindDir, Mode: uint32(info.Mode().Perm())})
		default:
			kind := "special file"
			switch {
			case info.Mode()&fs.ModeNamedPipe != 0:
				kind = "fifo"
			case info.Mode()&fs.ModeSocket != 0:
				kind = "socket"
			case info.Mode()&fs.ModeDevice != 0:
				kind = "device"
			}
			return &SpecialFileError{Path: rel, Kind: kind}
		}
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}

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
// with O_NOFOLLOW so a symlink swapped in after lstat cannot redirect the
// read, and the byte count — not the lstat size — bounds the limits, so a
// file growing mid-capture still fails closed.
func captureFile(p, rel string, size, maxFile int64, blobDir string) (string, int64, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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
	return hex.EncodeToString(blobHasher().Sum(data))
}

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
		// Idempotent: identical blob already stored.
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
