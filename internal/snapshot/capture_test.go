package snapshot

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

func TestCaptureBasicTree(t *testing.T) {
	source := t.TempDir()
	store := t.TempDir()
	writeTree(t, source, map[string]string{
		"README.md":         "hello\n",
		"src/main.go":       "package main\n",
		"docs/nested/a.txt": "aaa",
	})
	// An empty directory must survive as an explicit dir entry.
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o755); err != nil {
		t.Fatalf("snapshot: mkdir empty: %v", err)
	}
	// An executable binary file.
	bin := filepath.Join(source, "src", "tool")
	if err := os.WriteFile(bin, []byte{0x00, 0xff, 0x0a, 0x00}, 0o755); err != nil {
		t.Fatalf("snapshot: write bin: %v", err)
	}
	// A relative symlink inside the tree.
	if err := os.Symlink("../README.md", filepath.Join(source, "src", "up")); err != nil {
		t.Fatalf("snapshot: symlink: %v", err)
	}
	// An absolute symlink pointing outside the captured root.
	if err := os.Symlink("/etc/hostname", filepath.Join(source, "abs")); err != nil {
		t.Fatalf("snapshot: symlink: %v", err)
	}
	// A symlink to a directory outside the root: stored verbatim, never followed.
	outside := t.TempDir()
	writeTree(t, outside, map[string]string{"secret.txt": "nope"})
	if err := os.Symlink(outside, filepath.Join(source, "out")); err != nil {
		t.Fatalf("snapshot: symlink: %v", err)
	}

	m, err := Capture(context.Background(), source, store, CaptureOptions{})
	if err != nil {
		t.Fatalf("snapshot: capture: %v", err)
	}
	byPath := map[string]Entry{}
	for _, e := range m.Entries {
		byPath[e.Path] = e
	}
	for _, path := range []string{"README.md", "src", "src/main.go", "docs", "docs/nested", "docs/nested/a.txt", "empty", "src/tool", "src/up", "abs", "out"} {
		if _, ok := byPath[path]; !ok {
			t.Fatalf("snapshot: entry %s missing: %+v", path, m.Entries)
		}
	}
	if e := byPath["empty"]; e.Kind != "dir" {
		t.Fatalf("snapshot: empty dir not represented: %+v", e)
	}
	if e := byPath["src/tool"]; e.Kind != "file" || e.Mode != 0o755 || e.Size != 4 {
		t.Fatalf("snapshot: binary entry wrong: %+v", e)
	}
	if e := byPath["src/up"]; e.Kind != "symlink" || e.LinkTarget != "../README.md" {
		t.Fatalf("snapshot: symlink entry wrong: %+v", e)
	}
	if e := byPath["out"]; e.Kind != "symlink" || e.LinkTarget != outside {
		t.Fatalf("snapshot: out-of-root symlink not verbatim: %+v", e)
	}
	// The out-of-root directory's children must never be captured.
	if _, ok := byPath["out/secret.txt"]; ok {
		t.Fatal("snapshot: followed a symlink during capture")
	}
	if _, err := fingerprint.Parse(string(byPath["README.md"].Digest)); err != nil {
		t.Fatalf("snapshot: digest not canonical: %v", err)
	}

	// Blobs exist and carry exact bytes.
	blobDir := BlobDir(store)
	for path, content := range map[string]string{"README.md": "hello\n", "src/tool": "\x00\xff\x0a\x00"} {
		e := byPath[path]
		blob, err := os.ReadFile(filepath.Join(blobDir, string(e.Digest)))
		if err != nil {
			t.Fatalf("snapshot: blob of %s: %v", path, err)
		}
		if string(blob) != content {
			t.Fatalf("snapshot: blob of %s wrong: %q", path, blob)
		}
	}
	up := byPath["src/up"]
	blob, err := os.ReadFile(filepath.Join(blobDir, string(up.Digest)))
	if err != nil {
		t.Fatalf("snapshot: symlink blob: %v", err)
	}
	if string(blob) != "../README.md" {
		t.Fatalf("snapshot: symlink blob must be the target bytes, got %q", blob)
	}

	// Entries are sorted by path.
	for i := 1; i < len(m.Entries); i++ {
		if m.Entries[i-1].Path >= m.Entries[i].Path {
			t.Fatalf("snapshot: entries not sorted: %s then %s", m.Entries[i-1].Path, m.Entries[i].Path)
		}
	}
	if err := m.RootDigest.Validate(); err != nil {
		t.Fatalf("snapshot: root digest: %v", err)
	}
}

func TestCaptureBlobIdempotence(t *testing.T) {
	source, store := t.TempDir(), t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "aaa", "b/c.txt": "ccc"})

	m1, err := Capture(context.Background(), source, store, CaptureOptions{})
	if err != nil {
		t.Fatalf("snapshot: capture 1: %v", err)
	}
	before := dirFileCount(t, BlobDir(store))
	m2, err := Capture(context.Background(), source, store, CaptureOptions{})
	if err != nil {
		t.Fatalf("snapshot: capture 2: %v", err)
	}
	if m1.RootDigest != m2.RootDigest {
		t.Fatalf("snapshot: root digest changed between captures: %s vs %s", m1.RootDigest, m2.RootDigest)
	}
	if after := dirFileCount(t, BlobDir(store)); after != before {
		t.Fatalf("snapshot: second capture added blobs: %d then %d", before, after)
	}
}

func dirFileCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("snapshot: readdir %s: %v", dir, err)
	}
	return len(entries)
}

func TestCaptureExclusions(t *testing.T) {
	source, store := t.TempDir(), t.TempDir()
	writeTree(t, source, map[string]string{
		"keep.txt":            "keep",
		"a.log":               "log",
		"node_modules/pkg.js": "dep",
		"src/a.log":           "nested log",
		"src/keep.go":         "code",
	})
	m, err := Capture(context.Background(), source, store, CaptureOptions{
		Exclusions: []string{"node_modules", "*.log"},
	})
	if err != nil {
		t.Fatalf("snapshot: capture: %v", err)
	}
	byPath := map[string]bool{}
	for _, e := range m.Entries {
		byPath[e.Path] = true
	}
	if byPath["node_modules"] || byPath["node_modules/pkg.js"] {
		t.Fatal("snapshot: excluded directory captured")
	}
	if byPath["a.log"] {
		t.Fatal("snapshot: excluded root-level log captured")
	}
	if byPath["src/keep.go"] != true || byPath["src"] != true {
		t.Fatal("snapshot: kept files missing")
	}
	// "*.log" is a root-anchored glob: nested logs stay visible (documented).
	if !byPath["src/a.log"] {
		t.Fatal("snapshot: nested log should not match a root-level pattern")
	}

	for _, bad := range []string{"/abs", "../up", "a/../b", `a\b`, "", "[", "a\x00"} {
		_, err := Capture(context.Background(), source, store, CaptureOptions{Exclusions: []string{bad}})
		if !errors.Is(err, ErrInvalidExclusion) {
			t.Fatalf("snapshot: exclusion %q: got %v, want ErrInvalidExclusion", bad, err)
		}
	}
}

func TestCaptureLimits(t *testing.T) {
	source, store := t.TempDir(), t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "aaaa", "b.txt": "bbbb", "c.txt": "cccc"})
	ctx := context.Background()

	t.Run("entries", func(t *testing.T) {
		_, err := Capture(ctx, source, store, CaptureOptions{Limits: Limits{MaxEntries: 2}})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("snapshot: want ErrLimitExceeded, got %v", err)
		}
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != 2 {
			t.Fatalf("snapshot: limit error missing detail: %v", err)
		}
	})
	t.Run("file bytes", func(t *testing.T) {
		_, err := Capture(ctx, source, store, CaptureOptions{Limits: Limits{MaxFileBytes: 3}})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("snapshot: want ErrLimitExceeded, got %v", err)
		}
	})
	t.Run("tree bytes", func(t *testing.T) {
		_, err := Capture(ctx, source, store, CaptureOptions{Limits: Limits{MaxTreeBytes: 10}})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("snapshot: want ErrLimitExceeded, got %v", err)
		}
	})
	t.Run("zero means defaults", func(t *testing.T) {
		if _, err := Capture(ctx, source, store, CaptureOptions{Limits: Limits{}}); err != nil {
			t.Fatalf("snapshot: defaults rejected a tiny tree: %v", err)
		}
	})
}

func TestCaptureSpecialFiles(t *testing.T) {
	source, store := t.TempDir(), t.TempDir()

	fifo := filepath.Join(source, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("snapshot: mkfifo: %v", err)
	}
	_, err := Capture(context.Background(), source, store, CaptureOptions{})
	if !errors.Is(err, ErrSpecialFile) {
		t.Fatalf("snapshot: fifo: want ErrSpecialFile, got %v", err)
	}
	var sf *SpecialFileError
	if !errors.As(err, &sf) || sf.Path != "pipe" {
		t.Fatalf("snapshot: special error missing path: %v", err)
	}

	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "snap.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("snapshot: listen: %v", err)
	}
	defer ln.Close()
	_, err = Capture(context.Background(), sockDir, t.TempDir(), CaptureOptions{})
	if !errors.Is(err, ErrSpecialFile) {
		t.Fatalf("snapshot: socket: want ErrSpecialFile, got %v", err)
	}
}

func TestCaptureValidatesPaths(t *testing.T) {
	_, err := Capture(context.Background(), "does-not-exist", t.TempDir(), CaptureOptions{})
	if err == nil {
		t.Fatal("snapshot: missing source accepted")
	}
	if _, err := Capture(context.Background(), filepath.Join("rel", "ative"), t.TempDir(), CaptureOptions{}); err == nil {
		t.Fatal("snapshot: relative source accepted")
	}
	if _, err := Capture(context.Background(), t.TempDir(), filepath.Join("rel", "store"), CaptureOptions{}); err == nil {
		t.Fatal("snapshot: relative store accepted")
	}
}

func TestParallelCapturesShareStore(t *testing.T) {
	source, store := t.TempDir(), t.TempDir()
	writeTree(t, source, map[string]string{
		"one.txt":   strings.Repeat("1", 4096),
		"two.txt":   strings.Repeat("2", 4096),
		"three.bin": "\x00\x01\x02",
	})

	const n = 8
	digests := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, err := Capture(context.Background(), source, store, CaptureOptions{})
			if err == nil {
				digests[i] = string(m.RootDigest)
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("snapshot: capture %d: %v", i, errs[i])
		}
		if digests[i] != digests[0] {
			t.Fatalf("snapshot: capture %d digest %s differs from %s", i, digests[i], digests[0])
		}
	}
}
