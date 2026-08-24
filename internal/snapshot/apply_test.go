package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// captureTree is the test shorthand: capture source into a fresh store.
func captureTree(t *testing.T, source string, opts CaptureOptions) (Manifest, string) {
	t.Helper()
	store := t.TempDir()
	m, err := Capture(context.Background(), source, store, opts)
	if err != nil {
		t.Fatalf("snapshot: capture: %v", err)
	}
	return m, store
}

func TestMaterializeRoundTrip(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{
		"README.md":         "hello\n",
		"src/main.go":       "package main\n",
		"docs/nested/a.txt": "aaa",
	})
	if err := os.MkdirAll(filepath.Join(source, "empty", "deeper"), 0o755); err != nil {
		t.Fatalf("snapshot: mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin"), []byte{0x00, 0xff}, 0o755); err != nil {
		t.Fatalf("snapshot: write bin: %v", err)
	}
	outside := t.TempDir()
	links := map[string]string{
		"rel":  "docs/nested/a.txt",
		"up":   "../outside-root",
		"abs":  "/etc/hostname",
		"tree": outside,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(source, name)); err != nil {
			t.Fatalf("snapshot: symlink %s: %v", name, err)
		}
	}

	m, store := captureTree(t, source, CaptureOptions{})
	dest := filepath.Join(t.TempDir(), "clone")
	if err := Materialize(context.Background(), m, store, dest); err != nil {
		t.Fatalf("snapshot: materialize: %v", err)
	}

	// Every file byte-identical, every dir present (empty ones too), every
	// symlink verbatim, modes preserved despite the umask.
	for rel, content := range map[string]string{
		"README.md":         "hello\n",
		"src/main.go":       "package main\n",
		"docs/nested/a.txt": "aaa",
		"bin":               "\x00\xff",
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("snapshot: read %s: %v", rel, err)
		}
		if string(got) != content {
			t.Fatalf("snapshot: %s content %q, want %q", rel, got, content)
		}
	}
	if st, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !st.IsDir() {
		t.Fatalf("snapshot: empty dir missing: %v", err)
	}
	if st, err := os.Stat(filepath.Join(dest, "empty", "deeper")); err != nil || !st.IsDir() {
		t.Fatalf("snapshot: nested empty dir missing: %v", err)
	}
	if st, err := os.Stat(filepath.Join(dest, "bin")); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("snapshot: exec mode not preserved: %v", st.Mode())
	}
	for name, target := range links {
		got, err := os.Readlink(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("snapshot: readlink %s: %v", name, err)
		}
		if got != target {
			t.Fatalf("snapshot: symlink %s = %q, want %q", name, got, target)
		}
	}
	if err := Verify(context.Background(), dest, m); err != nil {
		t.Fatalf("snapshot: verify: %v", err)
	}
}

func TestMaterializeRefusesNonEmptyDestination(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "a"})
	m, store := captureTree(t, source, CaptureOptions{})

	dest := t.TempDir()
	writeTree(t, dest, map[string]string{"existing": "x"})
	err := Materialize(context.Background(), m, store, dest)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("snapshot: want ErrDestinationExists, got %v", err)
	}

	emptyDest := filepath.Join(t.TempDir(), "fresh")
	if err := Materialize(context.Background(), m, store, emptyDest); err != nil {
		t.Fatalf("snapshot: materialize into empty dir: %v", err)
	}
	// Materializing over an already-materialized tree is refused the same
	// way — it exists and is not empty.
	if err := Materialize(context.Background(), m, store, emptyDest); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("snapshot: re-materialize: want ErrDestinationExists, got %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "aaa", "d/b.txt": "bbb"})
	m, store := captureTree(t, source, CaptureOptions{})

	tamper := func(t *testing.T, mutate func(dest string)) {
		t.Helper()
		dest := filepath.Join(t.TempDir(), "tree")
		if err := Materialize(context.Background(), m, store, dest); err != nil {
			t.Fatalf("snapshot: materialize: %v", err)
		}
		mutate(dest)
		err := Verify(context.Background(), dest, m)
		if !errors.Is(err, ErrVerifyFailed) {
			t.Fatalf("snapshot: want ErrVerifyFailed, got %v", err)
		}
		var ve *VerifyError
		if !errors.As(err, &ve) || ve.Path == "" {
			t.Fatalf("snapshot: verify error lacks path: %v", err)
		}
	}
	t.Run("modified content", func(t *testing.T) {
		tamper(t, func(dest string) { _ = os.WriteFile(filepath.Join(dest, "a.txt"), []byte("XXX"), 0o644) })
	})
	t.Run("extra file", func(t *testing.T) {
		tamper(t, func(dest string) { _ = os.WriteFile(filepath.Join(dest, "extra"), []byte("x"), 0o644) })
	})
	t.Run("deleted file", func(t *testing.T) {
		tamper(t, func(dest string) { _ = os.Remove(filepath.Join(dest, "a.txt")) })
	})
	t.Run("mode change", func(t *testing.T) {
		tamper(t, func(dest string) { _ = os.Chmod(filepath.Join(dest, "a.txt"), 0o600) })
	})
	t.Run("replaced by symlink", func(t *testing.T) {
		tamper(t, func(dest string) {
			_ = os.Remove(filepath.Join(dest, "a.txt"))
			_ = os.Symlink("/etc/hostname", filepath.Join(dest, "a.txt"))
		})
	})
}

// fixture builds a base and result tree and returns base manifest, store,
// the diff patch, and the expected result manifest digest.
type diffFixture struct {
	base    Manifest
	result  Manifest
	store   string
	blobDir string
	patch   PatchManifest
}

// buildFixture captures base, then mutates the tree into result and
// diffs, leaving blobs of both trees in the store.
func buildFixture(t *testing.T, baseFiles map[string]string, mutate func(t *testing.T, dir string)) diffFixture {
	t.Helper()
	source := t.TempDir()
	writeTree(t, source, baseFiles)
	base, store := captureTree(t, source, CaptureOptions{})
	mutate(t, source)
	blobDir := BlobDir(store)
	patch, err := Diff(context.Background(), base, source, blobDir)
	if err != nil {
		t.Fatalf("snapshot: diff: %v", err)
	}
	result, err := Capture(context.Background(), source, store, CaptureOptions{})
	if err != nil {
		t.Fatalf("snapshot: capture result: %v", err)
	}
	return diffFixture{base: base, result: result, store: store, blobDir: blobDir, patch: patch}
}

func TestApplyPatchTransformsStage(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"keep.txt": "keep", "edit.txt": "old", "gone.txt": "gone", "dir/nested.txt": "nested"},
		func(t *testing.T, dir string) {
			writeTree(t, dir, map[string]string{"edit.txt": "new content", "added.txt": "brand new"})
			if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
				t.Fatalf("snapshot: remove: %v", err)
			}
			if err := os.Rename(filepath.Join(dir, "dir", "nested.txt"), filepath.Join(dir, "dir", "moved.txt")); err != nil {
				t.Fatalf("snapshot: rename: %v", err)
			}
		})

	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed stage: %v", err)
	}
	if err := Apply(context.Background(), stage, fx.blobDir, fx.patch); err != nil {
		t.Fatalf("snapshot: apply: %v", err)
	}
	if err := Verify(context.Background(), stage, fx.result); err != nil {
		t.Fatalf("snapshot: stage != result after apply: %v", err)
	}
}

func TestApplyIdempotent(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"a.txt": "aaa"},
		func(t *testing.T, dir string) { writeTree(t, dir, map[string]string{"a.txt": "bbb", "c.txt": "ccc"}) })
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	if err := Apply(context.Background(), stage, fx.blobDir, fx.patch); err != nil {
		t.Fatalf("snapshot: apply 1: %v", err)
	}
	if err := Apply(context.Background(), stage, fx.blobDir, fx.patch); err != nil {
		t.Fatalf("snapshot: apply 2: %v", err)
	}
	if err := Verify(context.Background(), stage, fx.result); err != nil {
		t.Fatalf("snapshot: second apply changed the tree: %v", err)
	}
}

func TestApplyStalePreimage(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"a.txt": "aaa", "b.txt": "bbb"},
		func(t *testing.T, dir string) {
			_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed"), 0o644)
		})
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	// Divergence at an untouched path does not block the operations, but
	// the final digest verification fails closed: the stage is not the
	// patch's result tree, and the divergent file was never overwritten.
	_ = os.WriteFile(filepath.Join(stage, "b.txt"), []byte("divergent"), 0o644)
	err := Apply(context.Background(), stage, fx.blobDir, fx.patch)
	if !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("snapshot: want ErrResultMismatch from final verify, got %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(stage, "b.txt"))
	if string(got) != "divergent" {
		t.Fatalf("snapshot: divergent content overwritten: %q", got)
	}
	_ = os.Remove(filepath.Join(stage, "b.txt"))

	stage2 := filepath.Join(t.TempDir(), "stage2")
	if err := Materialize(context.Background(), fx.base, fx.store, stage2); err != nil {
		t.Fatalf("snapshot: seed 2: %v", err)
	}
	_ = os.WriteFile(filepath.Join(stage2, "a.txt"), []byte("stale"), 0o644)
	err = Apply(context.Background(), stage2, fx.blobDir, fx.patch)
	if !errors.Is(err, ErrPatchPreimageMismatch) {
		t.Fatalf("snapshot: want ErrPatchPreimageMismatch, got %v", err)
	}
	var pm *PreimageMismatchError
	if !errors.As(err, &pm) || pm.Path != "a.txt" {
		t.Fatalf("snapshot: preimage error lacks path: %v", err)
	}
	// The divergent file was never overwritten.
	stale, _ := os.ReadFile(filepath.Join(stage2, "a.txt"))
	if string(stale) != "stale" {
		t.Fatalf("snapshot: divergent content overwritten: %q", stale)
	}
}

func TestApplyConflictsNeverOverwrite(t *testing.T) {
	base := Manifest{SchemaVersion: 1, Entries: []Entry{
		{Path: "d", Kind: "dir", Mode: 0o755},
		{Path: "d/x.txt", Kind: KindFile, Mode: 0o644},
	}}
	// content "new\n" under the blob domain.
	digest := fingerprint.Bytes(domainBlob, []byte("new\n"))
	store := t.TempDir()
	blobDir := BlobDir(store)
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		t.Fatalf("snapshot: mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, string(digest)), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("snapshot: blob: %v", err)
	}

	t.Run("add onto existing file", func(t *testing.T) {
		stage := t.TempDir()
		writeTree(t, stage, map[string]string{"added.txt": "existing divergent"})
		patch := PatchManifest{SchemaVersion: 1, BaseDigest: DigestManifest(base), ResultDigest: DigestManifest(base),
			Operations: []PatchOp{{Op: OpAdd, Path: "added.txt", Kind: KindFile, Mode: 0o644, Size: 4, Digest: string(digest)}}}
		err := Apply(context.Background(), stage, blobDir, patch)
		if !errors.Is(err, ErrPatchConflict) {
			t.Fatalf("snapshot: want ErrPatchConflict, got %v", err)
		}
		got, _ := os.ReadFile(filepath.Join(stage, "added.txt"))
		if string(got) != "existing divergent" {
			t.Fatalf("snapshot: existing content overwritten: %q", got)
		}
	})
	t.Run("delete missing path", func(t *testing.T) {
		stage := t.TempDir()
		patch := PatchManifest{SchemaVersion: 1, BaseDigest: DigestManifest(base), ResultDigest: DigestManifest(base),
			Operations: []PatchOp{{Op: OpDelete, Path: "ghost.txt", Kind: KindFile, Mode: 0o644, BeforeKind: KindFile, BeforeMode: 0o644, BeforeDigest: string(digest)}}}
		// A delete of an absent path is indistinguishable from (and
		// therefore counts as) already applied — roll-forward re-apply
		// must converge — so the refusal surfaces in the final digest
		// verification: the stage never was the patch's base tree.
		err := Apply(context.Background(), stage, blobDir, patch)
		if !errors.Is(err, ErrResultMismatch) {
			t.Fatalf("snapshot: want ErrResultMismatch, got %v", err)
		}
	})
	t.Run("add onto existing dir", func(t *testing.T) {
		stage := t.TempDir()
		if err := os.MkdirAll(filepath.Join(stage, "added"), 0o755); err != nil {
			t.Fatalf("snapshot: mkdir: %v", err)
		}
		patch := PatchManifest{SchemaVersion: 1, BaseDigest: DigestManifest(base), ResultDigest: DigestManifest(base),
			Operations: []PatchOp{{Op: OpAdd, Path: "added", Kind: KindFile, Mode: 0o644, Size: 4, Digest: string(digest)}}}
		if err := Apply(context.Background(), stage, blobDir, patch); !errors.Is(err, ErrPatchConflict) {
			t.Fatalf("snapshot: want ErrPatchConflict, got %v", err)
		}
	})
}

func TestApplyMissingBlob(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"a.txt": "aaa"},
		func(t *testing.T, dir string) { writeTree(t, dir, map[string]string{"a.txt": "changed"}) })
	// Remove the result blob so the apply cannot restore content.
	var afterDigest string
	for _, op := range fx.patch.Operations {
		if op.Path == "a.txt" {
			afterDigest = op.Digest
		}
	}
	if err := os.Remove(filepath.Join(fx.blobDir, afterDigest)); err != nil {
		t.Fatalf("snapshot: remove blob: %v", err)
	}
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	err := Apply(context.Background(), stage, fx.blobDir, fx.patch)
	if !errors.Is(err, ErrBlobMissing) {
		t.Fatalf("snapshot: want ErrBlobMissing, got %v", err)
	}
	// The stage was left untouched (two-phase apply).
	got, _ := os.ReadFile(filepath.Join(stage, "a.txt"))
	if string(got) != "aaa" {
		t.Fatalf("snapshot: stage mutated despite missing blob: %q", got)
	}
}

func TestApplyVerifiesResultDigest(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"a.txt": "aaa"},
		func(t *testing.T, dir string) { writeTree(t, dir, map[string]string{"b.txt": "bbb"}) })
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	lied := fx.patch
	lied.ResultDigest = fingerprint.Digest(strings.Repeat("f", 64))
	if err := Apply(context.Background(), stage, fx.blobDir, lied); !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("snapshot: want ErrResultMismatch, got %v", err)
	}
}

func TestApplyRejectsSymlinkParentEscape(t *testing.T) {
	stageRoot := t.TempDir()
	stage := filepath.Join(stageRoot, "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatalf("snapshot: mkdir stage: %v", err)
	}
	outside := filepath.Join(stageRoot, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("snapshot: mkdir outside: %v", err)
	}
	// A crafted patch plants a symlink and then a file "through" it.
	target := fingerprint.Bytes(domainBlob, []byte("pwned"))
	blobDir := BlobDir(t.TempDir())
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		t.Fatalf("snapshot: mkdir blobs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, string(target)), []byte("pwned"), 0o600); err != nil {
		t.Fatalf("snapshot: blob: %v", err)
	}
	seed := Manifest{SchemaVersion: 1}
	patch := PatchManifest{SchemaVersion: 1,
		BaseDigest:   DigestManifest(seed),
		ResultDigest: fingerprint.Digest(strings.Repeat("e", 64)),
		Operations: []PatchOp{
			{Op: OpAdd, Path: "escape", Kind: KindSymlink, Mode: 0o777, Size: 3, Digest: string(fingerprint.Bytes(domainBlob, []byte("../outside"))), LinkTarget: "../outside"},
			{Op: OpAdd, Path: "escape/pwned", Kind: KindFile, Mode: 0o644, Size: 5, Digest: string(target)},
		}}
	err := Apply(context.Background(), stage, blobDir, patch)
	if err == nil {
		t.Fatal("snapshot: symlink-parent escape accepted")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("snapshot: content escaped the stage: %v", entries)
	}
	if !errors.Is(err, ErrPatchConflict) && !errors.Is(err, ErrResultMismatch) {
		t.Fatalf("snapshot: escape must fail closed with a typed error, got %v", err)
	}
}

func TestApplyEmptyDirAndKindChange(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"plain": "file"},
		func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, "plain"))
			_ = os.Symlink("somewhere", filepath.Join(dir, "plain"))
			if err := os.MkdirAll(filepath.Join(dir, "newempty"), 0o750); err != nil {
				t.Fatalf("snapshot: mkdir: %v", err)
			}
		})
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	if err := Apply(context.Background(), stage, fx.blobDir, fx.patch); err != nil {
		t.Fatalf("snapshot: apply: %v", err)
	}
	if err := Verify(context.Background(), stage, fx.result); err != nil {
		t.Fatalf("snapshot: kind change / empty dir apply wrong: %v", err)
	}
	if st, err := os.Lstat(filepath.Join(stage, "newempty")); err != nil || st.Mode().Perm() != 0o750 {
		t.Fatalf("snapshot: added dir mode wrong: %v", st)
	}
}

func TestInvertPatchRestoresBase(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"edit.txt": "old", "gone.txt": "gone", "dir/nested.txt": "nested"},
		func(t *testing.T, dir string) {
			writeTree(t, dir, map[string]string{"edit.txt": "new", "added.txt": "added"})
			_ = os.Remove(filepath.Join(dir, "gone.txt"))
			_ = os.Rename(filepath.Join(dir, "dir", "nested.txt"), filepath.Join(dir, "dir", "moved.txt"))
			_ = os.MkdirAll(filepath.Join(dir, "newdir"), 0o755)
		})
	stage := filepath.Join(t.TempDir(), "stage")
	if err := Materialize(context.Background(), fx.base, fx.store, stage); err != nil {
		t.Fatalf("snapshot: seed: %v", err)
	}
	if err := Apply(context.Background(), stage, fx.blobDir, fx.patch); err != nil {
		t.Fatalf("snapshot: apply: %v", err)
	}
	inv, err := InvertPatch(fx.patch)
	if err != nil {
		t.Fatalf("snapshot: invert: %v", err)
	}
	if err := applyPatch(context.Background(), stage, fx.blobDir, inv, false); err != nil {
		t.Fatalf("snapshot: apply inverse: %v", err)
	}
	if err := Verify(context.Background(), stage, fx.base); err != nil {
		t.Fatalf("snapshot: inverse did not restore base: %v", err)
	}
	// Applying the inverse twice stays a no-op (idempotent revert).
	inv2, _ := InvertPatch(fx.patch)
	if err := applyPatch(context.Background(), stage, fx.blobDir, inv2, false); err != nil {
		t.Fatalf("snapshot: re-apply inverse: %v", err)
	}
	if err := Verify(context.Background(), stage, fx.base); err != nil {
		t.Fatalf("snapshot: re-applied inverse diverged: %v", err)
	}
}
