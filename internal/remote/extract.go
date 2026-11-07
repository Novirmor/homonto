package remote

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// Limits bound archive extraction to defeat tar/zip bombs and path attacks.
type Limits struct {
	MaxEntries    int   // maximum number of members
	MaxEntryBytes int64 // maximum bytes in any single regular file
	MaxTotalBytes int64 // maximum total regular-file bytes across the archive
}

// DefaultLimits are chosen well above real skill/agent bundles and well below
// resource exhaustion.
var DefaultLimits = Limits{
	MaxEntries:    10_000,
	MaxEntryBytes: 64 << 20,  // 64 MiB
	MaxTotalBytes: 256 << 20, // 256 MiB
}

// FileEntry is one regular file in a validated tree.
type FileEntry struct {
	Path string // clean, relative, forward-slash path
	Mode uint32 // executable bit preserved; others normalized at canonicalization
	Data []byte
}

// Tree is a validated archive: regular files only, sorted by Path.
type Tree struct {
	Files []FileEntry
}

// ValidateTarGz gunzips then validates a tar archive, bounding total decompressed
// bytes by lim.MaxTotalBytes so a decompression bomb is rejected while streaming.
func ValidateTarGz(r io.Reader, lim Limits) (Tree, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return Tree{}, fmt.Errorf("remote: gzip: %w", err)
	}
	defer zr.Close()
	// Bound decompressed input: one extra byte lets us detect an overflow read.
	bounded := &io.LimitedReader{R: zr, N: lim.MaxTotalBytes + 1}
	tree, err := ValidateTar(bounded, lim)
	if err != nil {
		return Tree{}, err
	}
	// tar stops at its end marker, before gzip necessarily checks its trailer.
	// Drain through the same bound to validate the checksum and count padding
	// and additional gzip members without allowing unbounded decompression.
	if _, err := io.Copy(io.Discard, bounded); err != nil {
		return Tree{}, fmt.Errorf("remote: gzip: %w", err)
	}
	if bounded.N == 0 {
		return Tree{}, fmt.Errorf("remote: archive exceeds the %d-byte decompressed cap", lim.MaxTotalBytes)
	}
	return tree, nil
}

// ValidateTar streams a tar archive, rejecting absolute paths, ".." traversal,
// non-regular members, duplicate paths, and any entry/total/count over the
// limits. It reads regular-file contents into memory (bounded by the limits) and
// returns them as a sorted Tree. It writes nothing to disk.
func ValidateTar(r io.Reader, lim Limits) (Tree, error) {
	tr := tar.NewReader(r)
	var (
		files []FileEntry
		total int64
		count int
		seen  = map[string]bool{}
	)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Tree{}, fmt.Errorf("remote: tar: %w", err)
		}
		count++
		if count > lim.MaxEntries {
			return Tree{}, fmt.Errorf("remote: archive exceeds %d entries", lim.MaxEntries)
		}

		// Standard tar commands include the archive root when packing `.`.
		// It is harmless only as a directory, not as a file or link.
		if hdr.Typeflag == tar.TypeDir && (hdr.Name == "." || hdr.Name == "./") {
			continue
		}

		clean, err := safeMemberPath(hdr.Name)
		if err != nil {
			return Tree{}, err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			continue // directories are implied by file paths
		case tar.TypeReg, '\x00':
			// regular file — '\x00' is the legacy (old GNU) regular-file flag
		default:
			return Tree{}, fmt.Errorf("remote: archive member %q is a non-regular type %q (symlinks, hardlinks, and devices are not allowed)", hdr.Name, string(hdr.Typeflag))
		}

		if clean == "" {
			return Tree{}, fmt.Errorf("remote: archive member has an empty path")
		}
		if seen[clean] {
			return Tree{}, fmt.Errorf("remote: archive has a duplicate member path %q", clean)
		}
		seen[clean] = true

		// Read bounded content. One extra byte over the per-entry cap detects an
		// overflow without allocating the whole oversized entry.
		limited := io.LimitReader(tr, lim.MaxEntryBytes+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return Tree{}, fmt.Errorf("remote: reading %q: %w", clean, err)
		}
		if int64(len(data)) > lim.MaxEntryBytes {
			return Tree{}, fmt.Errorf("remote: archive member %q exceeds the %d-byte per-entry cap", clean, lim.MaxEntryBytes)
		}
		total += int64(len(data))
		if total > lim.MaxTotalBytes {
			return Tree{}, fmt.Errorf("remote: archive exceeds the %d-byte total cap", lim.MaxTotalBytes)
		}

		mode := uint32(hdr.Mode) & 0o777
		files = append(files, FileEntry{Path: clean, Mode: mode, Data: data})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return Tree{Files: files}, nil
}

// safeMemberPath rejects absolute paths and any ".." traversal, returning a
// clean forward-slash relative path.
func safeMemberPath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	// Normalize separators; tar uses forward slashes but be defensive.
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("remote: archive member %q has an absolute path", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return "", fmt.Errorf("remote: archive member %q escapes the archive root", name)
	}
	// path.Clean cannot introduce a leading slash for a relative input, but guard.
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("remote: archive member %q resolves to an absolute path", name)
	}
	return clean, nil
}
