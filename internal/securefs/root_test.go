package securefs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// newRoot opens an anchor over a fresh temp directory and closes it when
// the test ends. It returns the Root and the on-disk anchor path so tests
// can seed the tree directly.
func newRoot(t *testing.T) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot(%s) = %v, want nil", dir, err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, dir
}

func diskPath(dir, rel string) string {
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// seedFile writes a file on disk with an exact mode, bypassing umask.
func seedFile(t *testing.T, path string, data []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func permOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

// patternBytes returns n deterministic non-repetitive-looking bytes.
func patternBytes(n int, seed uint32) []byte {
	b := make([]byte, n)
	x := seed
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Errorf("leftover temp file %s in %s", e.Name(), dir)
		}
	}
}

func TestOpenRootRejectsMissingEmptyAndNonDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenRoot(filepath.Join(dir, "missing")); err == nil {
		t.Error("OpenRoot of missing path: want error, got nil")
	}
	if _, err := OpenRoot(""); err == nil {
		t.Error(`OpenRoot(""): want error, got nil`)
	}
	f := filepath.Join(dir, "regular")
	seedFile(t, f, []byte("x"), 0o600)
	if _, err := OpenRoot(f); err == nil {
		t.Error("OpenRoot of regular file: want error, got nil")
	}
}

// TestRelGrammarIsEnforced: every operation must reject absolute paths,
// empty components, "." and "..", backslashes, and NUL before any syscall,
// and the rejection must leave the tree untouched.
func TestRelGrammarIsEnforced(t *testing.T) {
	root, dir := newRoot(t)
	bad := []string{
		"",
		"/abs",
		"/a",
		"a//b",
		"a/",
		".",
		"..",
		"a/./b",
		"a/../b",
		"./a",
		"../a",
		`a\b`,
		"a\x00b",
	}
	for _, rel := range bad {
		if err := root.WriteAtomic(rel, []byte("x"), 0o600); err == nil {
			t.Errorf("WriteAtomic(%q): want validation error, got nil", rel)
		}
		if _, err := root.ReadFile(rel); err == nil {
			t.Errorf("ReadFile(%q): want validation error, got nil", rel)
		}
		if err := root.CreateExclusive(rel, []byte("x"), 0o600); err == nil {
			t.Errorf("CreateExclusive(%q): want validation error, got nil", rel)
		}
		if err := root.Remove(rel); err == nil {
			t.Errorf("Remove(%q): want validation error, got nil", rel)
		}
		if rel != "" { // "" is valid for SyncDir: it names the anchor itself
			if err := root.SyncDir(rel); err == nil {
				t.Errorf("SyncDir(%q): want validation error, got nil", rel)
			}
		}
		if err := root.Rename("ok", rel); err == nil {
			t.Errorf("Rename(ok, %q): want validation error, got nil", rel)
		}
		if err := root.Rename(rel, "ok"); err == nil {
			t.Errorf("Rename(%q, ok): want validation error, got nil", rel)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("validation failures created entries: %v", entries)
	}
}

func TestWriteAtomicRoundTripAndNewFileMode(t *testing.T) {
	root, dir := newRoot(t)
	data := patternBytes(1<<20, 42)
	for name, mode := range map[string]fs.FileMode{
		"secret.txt": 0o600,
		"shared.txt": 0o644,
	} {
		if err := root.WriteAtomic(name, data, mode); err != nil {
			t.Fatalf("WriteAtomic(%s): %v", name, err)
		}
		got, err := root.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("%s: read back %d bytes, want identical %d", name, len(got), len(data))
		}
		if p := permOf(t, diskPath(dir, name)); p != mode {
			t.Errorf("%s: mode %o, want %o (umask must not apply)", name, p, mode)
		}
	}
}

// TestWriteAtomicPreservesExistingMode: an overwrite never rewrites the
// on-disk perm of an existing file; the mode argument only applies to new
// files.
func TestWriteAtomicPreservesExistingMode(t *testing.T) {
	root, dir := newRoot(t)
	cases := []struct {
		name    string
		onDisk  fs.FileMode
		offered fs.FileMode
	}{
		{"tight.bin", 0o600, 0o644},
		{"loose.bin", 0o644, 0o600},
	}
	for _, tc := range cases {
		path := diskPath(dir, tc.name)
		seedFile(t, path, []byte("old"), tc.onDisk)
		if err := root.WriteAtomic(tc.name, []byte("new"), tc.offered); err != nil {
			t.Fatalf("WriteAtomic(%s): %v", tc.name, err)
		}
		got, err := root.ReadFile(tc.name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", tc.name, err)
		}
		if string(got) != "new" {
			t.Errorf("%s: content %q, want %q", tc.name, got, "new")
		}
		if p := permOf(t, path); p != tc.onDisk {
			t.Errorf("%s: mode %o, want preserved %o", tc.name, p, tc.onDisk)
		}
	}
}

// TestWriteAtomicFailedWriteLeavesOriginalIntact: a write refused at the
// destination (a directory) must leave the tree byte-identical.
func TestWriteAtomicFailedWriteLeavesOriginalIntact(t *testing.T) {
	root, dir := newRoot(t)
	original := patternBytes(4096, 7)
	if err := root.WriteAtomic("orig.bin", original, 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := os.Mkdir(diskPath(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("target", []byte("clobber"), 0o600); err == nil {
		t.Fatal("WriteAtomic onto directory: want error, got nil")
	}
	got, err := root.ReadFile("orig.bin")
	if err != nil {
		t.Fatalf("ReadFile(orig.bin): %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Error("failed write altered the untouched neighbor file")
	}
	entries, err := os.ReadDir(diskPath(dir, "target"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("failed write left entries in target dir: %v", entries)
	}
}

func TestNonRegularFilesAreRefused(t *testing.T) {
	root, dir := newRoot(t)
	if err := os.Mkdir(diskPath(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.ReadFile("sub"); err == nil {
		t.Error("ReadFile of directory: want error, got nil")
	}
	pipe := diskPath(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	// Opening a fifo read-only blocks without a writer; both ops must
	// refuse without hanging.
	if _, err := root.ReadFile("pipe"); err == nil {
		t.Error("ReadFile of fifo: want error, got nil")
	}
	if err := root.WriteAtomic("pipe", []byte("x"), 0o600); err == nil {
		t.Error("WriteAtomic onto fifo: want error, got nil")
	}
	if err := root.CreateExclusive("pipe", []byte("x"), 0o600); err == nil {
		t.Error("CreateExclusive onto fifo: want error, got nil")
	}
}

func TestCreateExclusiveClaimsOnce(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.CreateExclusive("claim.lock", []byte("one"), 0o600); err != nil {
		t.Fatalf("CreateExclusive: %v", err)
	}
	got, err := root.ReadFile("claim.lock")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "one" {
		t.Errorf("content %q, want %q", got, "one")
	}
	if p := permOf(t, diskPath(dir, "claim.lock")); p != 0o600 {
		t.Errorf("mode %o, want 600 (umask must not apply)", p)
	}
	err = root.CreateExclusive("claim.lock", []byte("two"), 0o600)
	if !errors.Is(err, fs.ErrExist) {
		t.Errorf("second CreateExclusive: want fs.ErrExist, got %v", err)
	}
	got, err = root.ReadFile("claim.lock")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "one" {
		t.Errorf("content %q after rejected claim, want %q", got, "one")
	}
	if err := root.CreateExclusive("other.lock", []byte("x"), 0o644); err != nil {
		t.Fatalf("CreateExclusive: %v", err)
	}
	if p := permOf(t, diskPath(dir, "other.lock")); p != 0o644 {
		t.Errorf("mode %o, want 644", p)
	}
}

func TestRenameMovesAndReplaces(t *testing.T) {
	root, dir := newRoot(t)
	a := patternBytes(1024, 1)
	if err := root.WriteAtomic("a", a, 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := os.Mkdir(diskPath(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("a", "sub/b"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := root.ReadFile("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(a) after rename: want fs.ErrNotExist, got %v", err)
	}
	got, err := root.ReadFile("sub/b")
	if err != nil {
		t.Fatalf("ReadFile(sub/b): %v", err)
	}
	if !bytes.Equal(got, a) {
		t.Error("renamed file content changed")
	}
	if p := permOf(t, diskPath(dir, "sub/b")); p != 0o600 {
		t.Errorf("mode %o after rename, want 600", p)
	}

	b := patternBytes(512, 2)
	c := patternBytes(512, 3)
	if err := root.WriteAtomic("sub/b", b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("c", c, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("sub/b", "c"); err != nil {
		t.Fatalf("Rename over existing: %v", err)
	}
	got, err = root.ReadFile("c")
	if err != nil {
		t.Fatalf("ReadFile(c): %v", err)
	}
	if !bytes.Equal(got, b) {
		t.Error("rename over existing destination did not replace content")
	}
	if _, err := root.ReadFile("sub/b"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("old name still resolves after rename: %v", err)
	}
	if err := root.Rename("c", "/abs"); err == nil {
		t.Error(`Rename(c, "/abs"): want validation error, got nil`)
	}
}

func TestRemoveUnlinksFilesOnly(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.WriteAtomic("gone.bin", []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := root.Remove("gone.bin"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := root.ReadFile("gone.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile after Remove: want fs.ErrNotExist, got %v", err)
	}
	if err := root.Remove("gone.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("double Remove: want fs.ErrNotExist, got %v", err)
	}
	if err := os.Mkdir(diskPath(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.Remove("d"); err == nil {
		t.Error("Remove of directory: want error, got nil")
	}
	if _, err := os.Lstat(diskPath(dir, "d")); err != nil {
		t.Errorf("directory vanished after refused Remove: %v", err)
	}
}

func TestSyncDirAcceptsDirectoriesOnly(t *testing.T) {
	root, dir := newRoot(t)
	if err := root.SyncDir(""); err != nil {
		t.Errorf(`SyncDir(""): %v`, err)
	}
	if err := os.Mkdir(diskPath(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.SyncDir("sub"); err != nil {
		t.Errorf("SyncDir(sub): %v", err)
	}
	if err := root.WriteAtomic("f", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.SyncDir("f"); err == nil {
		t.Error("SyncDir of regular file: want error, got nil")
	}
}

// TestConcurrentWriteAtomicSamePath: concurrent writers must serialize as
// whole-content last-writer-wins; the file can never hold a mix of two
// writers' bytes. Run under -race.
func TestConcurrentWriteAtomicSamePath(t *testing.T) {
	root, dir := newRoot(t)
	const (
		writers = 8
		rounds  = 25
		size    = 1 << 16
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('A' + w)}, size)
			<-start
			for k := 0; k < rounds; k++ {
				if err := root.WriteAtomic("shared.bin", payload, 0o600); err != nil {
					t.Errorf("writer %d round %d: %v", w, k, err)
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()

	got, err := root.ReadFile("shared.bin")
	if err != nil {
		t.Fatalf("ReadFile after concurrency: %v", err)
	}
	if len(got) != size {
		t.Fatalf("length %d, want %d (partial write surfaced)", len(got), size)
	}
	for i := 1; i < len(got); i++ {
		if got[i] != got[0] {
			t.Fatalf("byte %d differs: content mixes two writers", i)
		}
	}
	assertNoTemps(t, dir)
}

// TestTempFilesNeverLinger: no operation, successful or failed, may leave a
// temp file behind.
func TestTempFilesNeverLinger(t *testing.T) {
	root, dir := newRoot(t)
	if err := os.Mkdir(diskPath(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("ok.bin", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("sub/ok.bin", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(diskPath(dir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteAtomic("target", []byte("x"), 0o600); err == nil {
		t.Fatal("WriteAtomic onto directory: want error, got nil")
	}
	long := strings.Repeat("n", 256)
	if err := root.WriteAtomic(long, []byte("x"), 0o600); err == nil {
		t.Fatal("WriteAtomic with overlong basename: want error, got nil")
	}
	assertNoTemps(t, dir)
	assertNoTemps(t, diskPath(dir, "sub"))
}

func TestCloseIsIdempotentAndShutsOperationsDown(t *testing.T) {
	dir := t.TempDir()
	root, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	if err := root.WriteAtomic("x", []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Errorf("second Close: %v, want nil", err)
	}
	if _, err := root.ReadFile("x"); err == nil {
		t.Error("ReadFile after Close: want error, got nil")
	}
	if err := root.WriteAtomic("y", []byte("x"), 0o600); err == nil {
		t.Error("WriteAtomic after Close: want error, got nil")
	}
}
