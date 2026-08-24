package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffNoChangeIsEmpty(t *testing.T) {
	source := t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "aaa"})
	base, store := captureTree(t, source, CaptureOptions{})

	patch, err := Diff(context.Background(), base, source, BlobDir(store))
	if err != nil {
		t.Fatalf("snapshot: diff: %v", err)
	}
	if len(patch.Operations) != 0 {
		t.Fatalf("snapshot: no-change diff produced ops: %+v", patch.Operations)
	}
	if patch.BaseDigest != base.RootDigest || patch.ResultDigest != base.RootDigest {
		t.Fatalf("snapshot: digests not recorded: %+v", patch)
	}
}

func TestDiffAddModifyDelete(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"keep.txt": "keep", "edit.txt": "old", "gone.txt": "gone"},
		func(t *testing.T, dir string) {
			writeTree(t, dir, map[string]string{"edit.txt": "new", "added.txt": "added"})
			if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
				t.Fatalf("snapshot: remove: %v", err)
			}
		})
	byPath := map[string]PatchOp{}
	for _, op := range fx.patch.Operations {
		byPath[op.Path] = op
	}
	if op := byPath["added.txt"]; op.Op != OpAdd || op.BeforeDigest != "" || op.Digest == "" {
		t.Fatalf("snapshot: add op wrong: %+v", op)
	}
	if op := byPath["edit.txt"]; op.Op != OpModify || op.BeforeDigest == "" || op.Digest == op.BeforeDigest {
		t.Fatalf("snapshot: modify op wrong: %+v", op)
	}
	if op := byPath["gone.txt"]; op.Op != OpDelete || op.BeforeDigest == "" || op.Digest != "" {
		t.Fatalf("snapshot: delete op wrong: %+v", op)
	}
	if _, ok := byPath["keep.txt"]; ok {
		t.Fatal("snapshot: unchanged path produced an op")
	}
	if fx.patch.BaseDigest != fx.base.RootDigest || fx.patch.ResultDigest != fx.result.RootDigest {
		t.Fatal("snapshot: diff digests not both recorded")
	}
	// Result blobs for added content were stored.
	for _, op := range fx.patch.Operations {
		if op.Op == OpAdd && op.Kind == KindFile {
			if _, err := os.Stat(filepath.Join(fx.blobDir, op.Digest)); err != nil {
				t.Fatalf("snapshot: added blob not stored: %v", err)
			}
		}
	}
}

func TestDiffRenameInference(t *testing.T) {
	t.Run("1:1 identical becomes rename", func(t *testing.T) {
		fx := buildFixture(t,
			map[string]string{"old/name.txt": "same content"},
			func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "new"), 0o755); err != nil {
					t.Fatalf("snapshot: mkdir: %v", err)
				}
				if err := os.Rename(filepath.Join(dir, "old", "name.txt"), filepath.Join(dir, "new", "name.txt")); err != nil {
					t.Fatalf("snapshot: rename: %v", err)
				}
			})
		if len(fx.patch.Operations) != 2 {
			t.Fatalf("snapshot: want rename + new-dir add, got %+v", fx.patch.Operations)
		}
		var op PatchOp
		renames := 0
		for _, candidate := range fx.patch.Operations {
			if candidate.Op == OpRename {
				op = candidate
				renames++
			}
		}
		if renames != 1 {
			t.Fatalf("snapshot: want exactly one rename op, got %+v", fx.patch.Operations)
		}
		if op.Op != OpRename || op.OldPath != "old/name.txt" || op.Path != "new/name.txt" {
			t.Fatalf("snapshot: rename op wrong: %+v", op)
		}
		if op.BeforeDigest != op.Digest {
			t.Fatalf("snapshot: rename digests differ: %+v", op)
		}
	})
	t.Run("ambiguous 2:2 stays delete+add", func(t *testing.T) {
		fx := buildFixture(t,
			map[string]string{"a1": "twin", "a2": "twin"},
			func(t *testing.T, dir string) {
				_ = os.Rename(filepath.Join(dir, "a1"), filepath.Join(dir, "b1"))
				_ = os.Rename(filepath.Join(dir, "a2"), filepath.Join(dir, "b2"))
			})
		kinds := map[string]int{}
		for _, op := range fx.patch.Operations {
			kinds[op.Op]++
		}
		if kinds[OpDelete] != 2 || kinds[OpAdd] != 2 || kinds[OpRename] != 0 {
			t.Fatalf("snapshot: ambiguous pair must stay delete+add: %+v", fx.patch.Operations)
		}
	})
	t.Run("mode change breaks rename", func(t *testing.T) {
		fx := buildFixture(t,
			map[string]string{"a": "twin"},
			func(t *testing.T, dir string) {
				_ = os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "b"))
				_ = os.Chmod(filepath.Join(dir, "b"), 0o755)
			})
		kinds := map[string]int{}
		for _, op := range fx.patch.Operations {
			kinds[op.Op]++
		}
		if kinds[OpDelete] != 1 || kinds[OpAdd] != 1 || kinds[OpRename] != 0 {
			t.Fatalf("snapshot: mode change must break the rename: %+v", fx.patch.Operations)
		}
	})
	t.Run("distinct 1:1 pairs both rename", func(t *testing.T) {
		fx := buildFixture(t,
			map[string]string{"x/one": "content-1", "y/two": "content-2"},
			func(t *testing.T, dir string) {
				_ = os.Rename(filepath.Join(dir, "x", "one"), filepath.Join(dir, "x", "uno"))
				_ = os.Rename(filepath.Join(dir, "y", "two"), filepath.Join(dir, "y", "dos"))
			})
		renames := 0
		for _, op := range fx.patch.Operations {
			if op.Op == OpRename {
				renames++
			}
		}
		if renames != 2 {
			t.Fatalf("snapshot: want 2 renames, got %+v", fx.patch.Operations)
		}
	})
}

func TestDiffKindChangeIsModify(t *testing.T) {
	fx := buildFixture(t,
		map[string]string{"plain": "file"},
		func(t *testing.T, dir string) {
			_ = os.Remove(filepath.Join(dir, "plain"))
			_ = os.Symlink("elsewhere", filepath.Join(dir, "plain"))
		})
	if len(fx.patch.Operations) != 1 || fx.patch.Operations[0].Op != OpModify {
		t.Fatalf("snapshot: kind change should be one modify: %+v", fx.patch.Operations)
	}
	op := fx.patch.Operations[0]
	if op.Kind != KindSymlink || op.BeforeKind != KindFile || op.LinkTarget != "elsewhere" {
		t.Fatalf("snapshot: kind-change op wrong: %+v", op)
	}
}

func TestDiffCaseCollisionFailsClosed(t *testing.T) {
	t.Run("case-only rename", func(t *testing.T) {
		source := t.TempDir()
		writeTree(t, source, map[string]string{"keep.txt": "same"})
		base, store := captureTree(t, source, CaptureOptions{})
		if err := os.Rename(filepath.Join(source, "keep.txt"), filepath.Join(source, "Keep.txt")); err != nil {
			t.Fatalf("snapshot: rename: %v", err)
		}
		_, err := Diff(context.Background(), base, source, BlobDir(store))
		if !errors.Is(err, ErrCaseCollision) {
			t.Fatalf("snapshot: want ErrCaseCollision, got %v", err)
		}
		var cc *CaseCollisionError
		if !errors.As(err, &cc) || (cc.A != "keep.txt" && cc.B != "keep.txt") {
			t.Fatalf("snapshot: collision error lacks paths: %v", err)
		}
	})
	t.Run("delete and add differing only in case", func(t *testing.T) {
		source := t.TempDir()
		writeTree(t, source, map[string]string{"a.txt": "one", "b.txt": "two"})
		base, store := captureTree(t, source, CaptureOptions{})
		_ = os.Remove(filepath.Join(source, "a.txt"))
		writeTree(t, source, map[string]string{"A.txt": "one"})
		_, err := Diff(context.Background(), base, source, BlobDir(store))
		if !errors.Is(err, ErrCaseCollision) {
			t.Fatalf("snapshot: want ErrCaseCollision, got %v", err)
		}
	})
	t.Run("case-distinct paths are fine", func(t *testing.T) {
		source := t.TempDir()
		writeTree(t, source, map[string]string{"a.txt": "one"})
		base, store := captureTree(t, source, CaptureOptions{})
		writeTree(t, source, map[string]string{"A.txt": "different"})
		if _, err := Diff(context.Background(), base, source, BlobDir(store)); err != nil {
			t.Fatalf("snapshot: distinct-case add must not collide: %v", err)
		}
	})
}

func TestValidateScope(t *testing.T) {
	patch := PatchManifest{SchemaVersion: 1, Operations: []PatchOp{
		{Op: OpModify, Path: "src/a.go"},
		{Op: OpRename, Path: "src/moved.go", OldPath: "src/old.go"},
		{Op: OpAdd, Path: "docs/readme.md"},
	}}
	if err := ValidateScope(patch, nil); err != nil {
		t.Fatalf("snapshot: empty scope must be unrestricted: %v", err)
	}
	if err := ValidateScope(patch, []string{"src", "docs"}); err != nil {
		t.Fatalf("snapshot: multi-entry scope: %v", err)
	}
	if err := ValidateScope(patch, []string{"."}); err != nil {
		t.Fatalf("snapshot: dot scope is the whole tree: %v", err)
	}
	err := ValidateScope(patch, []string{"lib"})
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("snapshot: want ErrScopeViolation, got %v", err)
	}
	var sv *ScopeViolationError
	if !errors.As(err, &sv) || len(sv.Paths) != 4 {
		t.Fatalf("snapshot: violation must list every offender (rename counts both paths): %v", err)
	}
	// A rename whose destination is in scope but whose source is not
	// still violates.
	err = ValidateScope(PatchManifest{SchemaVersion: 1, Operations: []PatchOp{
		{Op: OpRename, Path: "in/x", OldPath: "out/x"},
	}}, []string{"in"})
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("snapshot: rename source outside scope: %v", err)
	}
	for _, bad := range []string{"/abs", "../up"} {
		if err := ValidateScope(patch, []string{bad}); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("snapshot: scope %q: want ErrInvalidPath, got %v", bad, err)
		}
	}
}
