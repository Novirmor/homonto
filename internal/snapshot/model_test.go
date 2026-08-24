package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// mustUUID mints a valid typed identifier.
func mustUUID[T ~string](t *testing.T, new func() (T, error)) T {
	t.Helper()
	id, err := new()
	if err != nil {
		t.Fatalf("snapshot: id: %v", err)
	}
	return id
}

// validManifestDoc builds a manifest JSON document from an entry list.
func validManifestDoc(t *testing.T, entries []Entry) string {
	t.Helper()
	m := Manifest{SchemaVersion: 1, Entries: entries}
	m.RootDigest = DigestManifest(m)
	b, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("snapshot: encode: %v", err)
	}
	return string(b)
}

func TestDecodeManifestAcceptsCanonicalDoc(t *testing.T) {
	doc := validManifestDoc(t, []Entry{
		{Path: "a", Kind: "dir", Mode: 0o700},
		{Path: "a/f.txt", Kind: "file", Mode: 0o644, Size: 3, Digest: strings.Repeat("0", 64)},
		{Path: "l", Kind: "symlink", Mode: 0o777, Size: 1, Digest: strings.Repeat("1", 64), LinkTarget: "a"},
	})
	m, err := DecodeManifest([]byte(doc))
	if err != nil {
		t.Fatalf("snapshot: decode: %v", err)
	}
	if len(m.Entries) != 3 || m.Entries[0].Path != "a" {
		t.Fatalf("snapshot: entries not decoded: %+v", m.Entries)
	}
}

func TestDecodeManifestRejectsMalformed(t *testing.T) {
	good := []Entry{
		{Path: "d", Kind: "dir", Mode: 0o700},
		{Path: "d/f", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
	}
	cases := []struct {
		name string
		doc  string
		want error
	}{
		{"unknown field", `{"schema_version":1,"repository_id":"","root_digest":"","entries":[],"bogus":1}`, ErrInvalidManifest},
		{"schema zero", `{"schema_version":0,"repository_id":"","root_digest":"","entries":[]}`, ErrUnsupportedSchema},
		{"schema two", `{"schema_version":2,"repository_id":"","root_digest":"","entries":[]}`, ErrUnsupportedSchema},
		{"two documents", validManifestDoc(t, good) + validManifestDoc(t, good), ErrInvalidManifest},
		{"not an object", `[]`, ErrInvalidManifest},
		{"trailing junk", validManifestDoc(t, good) + "x", ErrInvalidManifest},
		{"unsorted entries", `{"schema_version":1,"repository_id":"","root_digest":"` +
			string(DigestManifest(Manifest{SchemaVersion: 1, Entries: []Entry{
				{Path: "a", Kind: "dir", Mode: 0o700},
				{Path: "z", Kind: "dir", Mode: 0o700},
			}})) + `","entries":[{"path":"z","kind":"dir","mode":448},{"path":"a","kind":"dir","mode":448}]}`, ErrInvalidManifest},
		{"duplicate paths", validManifestDoc(t, []Entry{
			{Path: "a", Kind: "dir", Mode: 0o700},
			{Path: "a", Kind: "dir", Mode: 0o700},
		}), ErrDuplicatePath},
		{"bad digest hex", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o644, Size: 1, Digest: "XYZ"},
		}), ErrInvalidManifest},
		{"uppercase digest", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("A", 64)},
		}), ErrInvalidManifest},
		{"absolute path", validManifestDoc(t, []Entry{
			{Path: "/etc/passwd", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidPath},
		{"dotdot path", validManifestDoc(t, []Entry{
			{Path: "../escape", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidPath},
		{"backslash path", validManifestDoc(t, []Entry{
			{Path: `a\b`, Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidPath},
		{"nul path", validManifestDoc(t, []Entry{
			{Path: "a\x00b", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidPath},
		{"dirty path", validManifestDoc(t, []Entry{
			{Path: "a//b", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidPath},
		{"mode too wide", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o1000, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidManifest},
		{"unknown kind", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "fifo", Mode: 0o644},
		}), ErrInvalidManifest},
		{"symlink without target", validManifestDoc(t, []Entry{
			{Path: "l", Kind: "symlink", Mode: 0o777, Size: 1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidManifest},
		{"file with target", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64), LinkTarget: "x"},
		}), ErrInvalidManifest},
		{"dir with digest", validManifestDoc(t, []Entry{
			{Path: "d", Kind: "dir", Mode: 0o700, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidManifest},
		{"dir with size", validManifestDoc(t, []Entry{
			{Path: "d", Kind: "dir", Mode: 0o700, Size: 4},
		}), ErrInvalidManifest},
		{"file without digest", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o644, Size: 1},
		}), ErrInvalidManifest},
		{"negative size", validManifestDoc(t, []Entry{
			{Path: "f", Kind: "file", Mode: 0o644, Size: -1, Digest: strings.Repeat("a", 64)},
		}), ErrInvalidManifest},
		{"nul in link target", validManifestDoc(t, []Entry{
			{Path: "l", Kind: "symlink", Mode: 0o777, Size: 1, Digest: strings.Repeat("a", 64), LinkTarget: "a\x00"},
		}), ErrInvalidManifest},
		{"root digest mismatch", func() string {
			m := Manifest{SchemaVersion: 1, Entries: good, RootDigest: fingerprint.Digest(strings.Repeat("b", 64))}
			b, _ := EncodeManifest(m)
			return string(b)
		}(), ErrDigestMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeManifest([]byte(tc.doc))
			if err == nil {
				t.Fatal("snapshot: decode accepted malformed doc")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("snapshot: error %v is not %v", err, tc.want)
			}
		})
	}
}

func TestDecodeManifestRejectsBadRepositoryID(t *testing.T) {
	entries := []Entry{{Path: "f", Kind: "file", Mode: 0o644, Size: 1, Digest: strings.Repeat("a", 64)}}
	m := Manifest{SchemaVersion: 1, RepositoryID: identity.RepositoryID("not-a-uuid"), Entries: entries}
	m.RootDigest = DigestManifest(m)
	b, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("snapshot: encode: %v", err)
	}
	if _, err := DecodeManifest(b); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("snapshot: error %v is not %v", err, ErrInvalidManifest)
	}
}

func TestManifestEncodeDecodeRoundTrip(t *testing.T) {
	repo := mustUUID(t, identity.NewRepositoryID)
	m := Manifest{
		SchemaVersion: 1,
		RepositoryID:  repo,
		Entries: []Entry{
			{Path: "d", Kind: "dir", Mode: 0o755},
			{Path: "d/bin", Kind: "file", Mode: 0o755, Size: 4, Digest: strings.Repeat("c", 64)},
			{Path: "l", Kind: "symlink", Mode: 0o777, Size: 2, Digest: strings.Repeat("d", 64), LinkTarget: "d/"},
		},
	}
	m.RootDigest = DigestManifest(m)
	b, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("snapshot: encode: %v", err)
	}
	got, err := DecodeManifest(b)
	if err != nil {
		t.Fatalf("snapshot: decode: %v", err)
	}
	if got.RootDigest != m.RootDigest || len(got.Entries) != 3 {
		t.Fatalf("snapshot: round trip lost data: %+v", got)
	}
	// The encoding must stay canonical: re-encoding reproduces the bytes.
	again, err := EncodeManifest(got)
	if err != nil {
		t.Fatalf("snapshot: re-encode: %v", err)
	}
	if !bytes.Equal(b, again) {
		t.Fatalf("snapshot: encoding not canonical:\n%s\n%s", b, again)
	}
}

func TestDigestManifestIgnoresEntryOrderAndRepoID(t *testing.T) {
	entries := []Entry{
		{Path: "a", Kind: "dir", Mode: 0o700},
		{Path: "a/f", Kind: "file", Mode: 0o600, Size: 2, Digest: strings.Repeat("e", 64)},
	}
	reversed := []Entry{entries[1], entries[0]}
	d1 := DigestManifest(Manifest{SchemaVersion: 1, Entries: entries})
	d2 := DigestManifest(Manifest{SchemaVersion: 1, Entries: reversed})
	if d1 != d2 {
		t.Fatalf("snapshot: digest depends on entry order: %s vs %s", d1, d2)
	}
	repo := mustUUID(t, identity.NewRepositoryID)
	d3 := DigestManifest(Manifest{SchemaVersion: 1, RepositoryID: repo, Entries: entries})
	if d1 != d3 {
		t.Fatalf("snapshot: digest must cover the tree, not repository metadata")
	}
}

// writeTree materializes a fixture tree of files (path -> content).
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("snapshot: mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("snapshot: write %s: %v", rel, err)
		}
	}
}

// readManifestFileAsJSON decodes a manifest file leniently, for negative tests.
func jsonDoc(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("snapshot: marshal: %v", err)
	}
	return b
}
