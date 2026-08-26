// Package snapshot captures non-Git source trees into immutable,
// content-addressed snapshots and derives deterministic patch manifests
// between a base snapshot and a result tree.
//
// # Storage layout
//
// All paths live under one store root (for a member integration:
// <root>/.homonto/integrations/<work-id>/<repository-id>):
//
//	store/snapshots/<action-id>/base/manifest.json   the base snapshot
//	store/snapshots/<action-id>/work/                the implementer's tree
//	store/patches/<action-id>/manifest.json          the result patch
//	store/blobs/sha256/<digest>                      content-addressed blobs
//	store/stage/                                     the integration stage
//
// This package owns creating these directories (0700).
//
// # Manifest model
//
// A Manifest (schema v1, strict JSON) lists every entry of a tree sorted by
// path: files and symlinks carry a content digest under the blob domain,
// directories are explicit entries — including empty directories, which Git
// cannot represent but snapshots must round-trip. root_digest is a
// domain-separated digest over schema version and entries (not repository
// metadata): two trees with equal content have equal root digests.
//
// Symlinks are never followed during capture; their target string is stored
// verbatim as data (digest and blob of the target bytes) — including
// absolute or ..-escaping targets. Materialize recreates them verbatim;
// scope validation treats the link entry itself as in-scope, never its
// target. Only permission bits (0..0777) are preserved; setuid/setgid and
// other special bits are dropped, and symlink modes are informational (the
// OS assigns them; materialize never chmods a symlink).
//
// # Patch model
//
// Diff compares a base manifest against a fresh capture of the result tree
// and emits a PatchManifest (schema v1, strict JSON) of add/modify/delete/
// rename operations. Rename inference is deterministic: a pure delete and a
// pure add with identical content digest AND mode pair into one rename only
// when each is the sole entry with that (digest, mode); anything ambiguous
// stays delete+add. Every operation records its before and after digests.
// Patches always fail closed on case-insensitive path collisions among
// operation paths — on any filesystem — because applying such a patch on
// macOS or Windows would clobber one of the two.
package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Schema version of manifests and patch manifests.
const SchemaVersion = 1

// Entry kinds.
const (
	KindFile    = "file"
	KindSymlink = "symlink"
	KindDir     = "dir"
)

// Domains separating this package's digests from every other use.
const (
	domainBlob     = "homonto.v1.snapshot.blob"
	domainManifest = "homonto.v1.snapshot.manifest"
)

// Typed errors. Wrap with context via fmt.Errorf("%w", ...) so callers can
// branch with errors.Is and read details with errors.As.
var (
	// ErrInvalidManifest: a manifest fails structural validation.
	ErrInvalidManifest = errors.New("snapshot: invalid manifest")
	// ErrUnsupportedSchema: a document names an unknown schema version.
	ErrUnsupportedSchema = errors.New("snapshot: unsupported schema version")
	// ErrInvalidPatch: a patch manifest fails structural validation.
	ErrInvalidPatch = errors.New("snapshot: invalid patch manifest")
	// ErrInvalidPath: a path is not slash-clean, relative, and safe.
	ErrInvalidPath = errors.New("snapshot: invalid path")
	// ErrDuplicatePath: two entries normalize to the same path.
	ErrDuplicatePath = errors.New("snapshot: duplicate path")
	// ErrDigestMismatch: a manifest's root digest does not match its entries.
	ErrDigestMismatch = errors.New("snapshot: manifest digest mismatch")
	// ErrInvalidExclusion: a capture exclusion pattern is malformed.
	ErrInvalidExclusion = errors.New("snapshot: invalid exclusion pattern")
	// ErrLimitExceeded: a capture crossed a declared limit.
	ErrLimitExceeded = errors.New("snapshot: capture limit exceeded")
	// ErrSpecialFile: capture found a FIFO, socket, or device.
	ErrSpecialFile = errors.New("snapshot: special files are not snapshotable")
	// ErrDestinationExists: materialize destination exists and is not empty.
	ErrDestinationExists = errors.New("snapshot: destination exists and is not empty")
	// ErrBlobMissing: a required content blob is not in the store.
	ErrBlobMissing = errors.New("snapshot: content blob missing from store")
	// ErrPatchPreimageMismatch: stage content does not match an operation's
	// recorded preimage.
	ErrPatchPreimageMismatch = errors.New("snapshot: patch preimage mismatch")
	// ErrPatchConflict: stage structure conflicts with an operation
	// (exists where absent was expected, or vice versa).
	ErrPatchConflict = errors.New("snapshot: stage conflicts with patch")
	// ErrResultMismatch: the stage tree digest does not equal the patch's
	// recorded result digest after applying.
	ErrResultMismatch = errors.New("snapshot: stage does not match patch result")
	// ErrVerifyFailed: a tree does not match the manifest it is verified
	// against.
	ErrVerifyFailed = errors.New("snapshot: tree does not match manifest")
	// ErrScopeViolation: patch operations fall outside the declared scope.
	ErrScopeViolation = errors.New("snapshot: changed paths outside declared scope")
	// ErrCaseCollision: operation paths collide case-insensitively.
	ErrCaseCollision = errors.New("snapshot: case-insensitive path collision")
	// ErrSourceNotDirectory: a capture/diff source root is not a real
	// directory (a regular file, or a symlink to a directory).
	ErrSourceNotDirectory = errors.New("snapshot: source is not a directory")
	// ErrBlobCorrupt: a stored blob's bytes do not match its digest.
	ErrBlobCorrupt = errors.New("snapshot: stored blob is corrupt")
)

// ManifestError carries the reason a manifest is invalid.
type ManifestError struct {
	Reason string
}

func (e *ManifestError) Error() string {
	return fmt.Sprintf("snapshot: invalid manifest: %s", e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ManifestError) Unwrap() error { return ErrInvalidManifest }

// DuplicatePathError names the duplicated path.
type DuplicatePathError struct {
	Path string
}

func (e *DuplicatePathError) Error() string {
	return fmt.Sprintf("snapshot: duplicate path %q", e.Path)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *DuplicatePathError) Unwrap() error { return ErrDuplicatePath }

// InvalidExclusionError names the rejected exclusion pattern.
type InvalidExclusionError struct {
	Pattern string
	Reason  string
}

func (e *InvalidExclusionError) Error() string {
	return fmt.Sprintf("snapshot: exclusion %q rejected: %s", e.Pattern, e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *InvalidExclusionError) Unwrap() error { return ErrInvalidExclusion }

// LimitError names which limit was crossed, by what, and where.
type LimitError struct {
	What   string
	Path   string
	Limit  int64
	Actual int64
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("snapshot: %s limit %d exceeded at %q: %d", e.What, e.Limit, e.Path, e.Actual)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *LimitError) Unwrap() error { return ErrLimitExceeded }

// SpecialFileError names the special entry (FIFO, socket, device).
type SpecialFileError struct {
	Path string
	Kind string
}

func (e *SpecialFileError) Error() string {
	return fmt.Sprintf("snapshot: %s %q is a special file; capture fails closed", e.Kind, e.Path)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *SpecialFileError) Unwrap() error { return ErrSpecialFile }

// BlobMissingError names the missing blob digest.
type BlobMissingError struct {
	Digest string
}

func (e *BlobMissingError) Error() string {
	return fmt.Sprintf("snapshot: blob %s missing from store", e.Digest)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *BlobMissingError) Unwrap() error { return ErrBlobMissing }

// VerifyError names the first entry where a tree and manifest disagree.
type VerifyError struct {
	Path   string
	Reason string
}

func (e *VerifyError) Error() string {
	return fmt.Sprintf("snapshot: verify failed at %q: %s", e.Path, e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *VerifyError) Unwrap() error { return ErrVerifyFailed }

// SourceNotDirectoryError names the source root that is not a real
// directory and what lstat saw instead.
type SourceNotDirectoryError struct {
	Path string
	Got  string // e.g. "regular file", "symlink"
}

func (e *SourceNotDirectoryError) Error() string {
	return fmt.Sprintf("snapshot: source %s is a %s, not a directory (symlinks are never followed)", e.Path, e.Got)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *SourceNotDirectoryError) Unwrap() error { return ErrSourceNotDirectory }

// BlobCorruptError names the blob whose stored bytes do not hash to its
// own digest.
type BlobCorruptError struct {
	Digest string
}

func (e *BlobCorruptError) Error() string {
	return fmt.Sprintf("snapshot: blob %s does not match its digest", e.Digest)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *BlobCorruptError) Unwrap() error { return ErrBlobCorrupt }

// Entry is one manifest entry: a file, symlink, or directory of the
// captured tree, root-relative and slash-clean.
type Entry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Mode holds the permission bits 0..0777. For symlinks it is
	// informational (the OS assigns symlink modes; materialize never
	// chmods a symlink).
	Mode uint32 `json:"mode"`
	// Size is the content size in bytes: file length, symlink target
	// length. Zero (omitted) for directories.
	Size int64 `json:"size,omitempty"`
	// Digest is the content digest (blob domain): file bytes, or the
	// symlink target bytes. Empty (omitted) for directories.
	Digest string `json:"digest,omitempty"`
	// LinkTarget is the symlink target, verbatim (it may be absolute or
	// contain ..). Empty (omitted) for other kinds.
	LinkTarget string `json:"link_target,omitempty"`
}

// Manifest is the schema v1 snapshot of one tree.
type Manifest struct {
	SchemaVersion int                   `json:"schema_version"`
	RepositoryID  identity.RepositoryID `json:"repository_id"`
	RootDigest    fingerprint.Digest    `json:"root_digest"`
	// Entries are sorted by path; every directory — including empty
	// ones — is an explicit entry.
	Entries []Entry `json:"entries"`
}

// BlobDir returns the directory holding content blobs under store:
// store/blobs/sha256/<digest>.
func BlobDir(store string) string { return path.Join(store, "blobs", "sha256") }

// DigestManifest computes the root digest of a manifest's tree: a
// domain-separated digest over schema version and entries sorted by path.
// Repository metadata is deliberately not covered — equal trees digest
// equal.
func DigestManifest(m Manifest) fingerprint.Digest {
	entries := make([]Entry, len(m.Entries))
	copy(entries, m.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	view := struct {
		SchemaVersion int     `json:"schema_version"`
		Entries       []Entry `json:"entries"`
	}{m.SchemaVersion, entries}
	d, err := fingerprint.CanonicalJSON(domainManifest, view)
	if err != nil {
		// CanonicalJSON only fails on unmarshalable values; Entry is
		// plain data. A digest of last resort keeps the API total.
		return fingerprint.Digest("")
	}
	return d
}

// EncodeManifest writes the canonical JSON encoding of m (sorted entries,
// trailing newline). ValidateManifest must have accepted m.
func EncodeManifest(m Manifest) ([]byte, error) {
	entries := make([]Entry, len(m.Entries))
	copy(entries, m.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	m.Entries = entries
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("snapshot: encode manifest: %w", err)
	}
	return buf.Bytes(), nil
}

// DecodeManifest strictly decodes one manifest document: unknown fields,
// trailing data, non-canonical digests, unsafe paths, duplicate or unsorted
// entries, and digest inconsistencies are typed errors.
func DecodeManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, &ManifestError{Reason: fmt.Sprintf("decode: %v", err)}
	}
	if dec.More() {
		return Manifest{}, &ManifestError{Reason: "trailing data after manifest"}
	}
	if m.Entries == nil {
		m.Entries = []Entry{} // canonical form: "entries":[] not null
	}
	if err := ValidateManifest(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// ValidateManifest checks every structural rule of a manifest, including
// that RootDigest matches its entries and that no two entries collide
// case-insensitively (mirroring the patch rule — such a tree cannot
// round-trip on a case-insensitive filesystem).
func ValidateManifest(m Manifest) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("snapshot: manifest schema version %d: %w", m.SchemaVersion, ErrUnsupportedSchema)
	}
	if m.RepositoryID != "" {
		if err := identity.ValidateUUID(string(m.RepositoryID)); err != nil {
			return &ManifestError{Reason: fmt.Sprintf("repository_id: %v", err)}
		}
	}
	if err := m.RootDigest.Validate(); err != nil {
		return &ManifestError{Reason: fmt.Sprintf("root_digest: %v", err)}
	}
	for i, e := range m.Entries {
		if i > 0 && m.Entries[i-1].Path >= e.Path {
			if m.Entries[i-1].Path == e.Path {
				return &DuplicatePathError{Path: e.Path}
			}
			return &ManifestError{Reason: fmt.Sprintf("entries not sorted: %s before %s", m.Entries[i-1].Path, e.Path)}
		}
		if err := validateEntry(e); err != nil {
			return err
		}
	}
	paths := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		paths[i] = e.Path
	}
	if err := checkCaseCollisionPaths(paths); err != nil {
		return err
	}
	if DigestManifest(m) != m.RootDigest {
		return fmt.Errorf("snapshot: manifest root digest %s does not cover its entries: %w", m.RootDigest, ErrDigestMismatch)
	}
	return nil
}

// validateEntry checks one entry's shape and path.
func validateEntry(e Entry) error {
	if err := ValidateRelPath(e.Path); err != nil {
		return err
	}
	if e.Mode > 0o777 {
		return &ManifestError{Reason: fmt.Sprintf("entry %s: mode %#o exceeds 0777", e.Path, e.Mode)}
	}
	switch e.Kind {
	case KindFile:
		if e.Digest == "" || e.Size < 0 || e.LinkTarget != "" {
			return &ManifestError{Reason: fmt.Sprintf("file entry %s: needs digest, non-negative size, no link target", e.Path)}
		}
		if err := mustDigest(e.Digest, e.Path); err != nil {
			return err
		}
	case KindSymlink:
		if e.LinkTarget == "" || e.Size < 0 || e.Digest == "" {
			return &ManifestError{Reason: fmt.Sprintf("symlink entry %s: needs target, digest, non-negative size", e.Path)}
		}
		if err := mustDigest(e.Digest, e.Path); err != nil {
			return err
		}
		if strings.ContainsRune(e.LinkTarget, 0) {
			return &ManifestError{Reason: fmt.Sprintf("symlink entry %s: target contains NUL", e.Path)}
		}
	case KindDir:
		if e.Digest != "" || e.Size != 0 || e.LinkTarget != "" {
			return &ManifestError{Reason: fmt.Sprintf("dir entry %s: must not carry content fields", e.Path)}
		}
	default:
		return &ManifestError{Reason: fmt.Sprintf("entry %s: unknown kind %q", e.Path, e.Kind)}
	}
	return nil
}

func mustDigest(digest, at string) error {
	if _, err := fingerprint.Parse(digest); err != nil {
		return &ManifestError{Reason: fmt.Sprintf("entry %s: digest: %v", at, err)}
	}
	return nil
}
