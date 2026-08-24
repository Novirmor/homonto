package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestSourceFingerprintStable proves the baseline digest is deterministic
// for identical repository state.
func TestSourceFingerprintStable(t *testing.T) {
	e := newEnv(t)
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp1 == "" {
		t.Error("fingerprint is empty")
	}
	if fp1 != fp2 {
		t.Errorf("fingerprints differ for identical state: %s vs %s", fp1, fp2)
	}
}

// TestSourceFingerprintTracksExecutableBit proves mode is part of the
// digest: flipping the exec bit changes it, and flipping it back restores
// the exact baseline.
func TestSourceFingerprintTracksExecutableBit(t *testing.T) {
	e := newEnv(t)
	commitFile(t, e.member, "bin/tool.sh", "#!/bin/sh\necho hi\n", "add tool")
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	tool := filepath.Join(e.member, "bin", "tool.sh")
	if err := os.Chmod(tool, 0o755); err != nil {
		t.Fatalf("chmod +x: %v", err)
	}
	commitAll(t, e.member, "make tool executable")
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp2 == fp1 {
		t.Error("fingerprint unchanged after chmod +x, want change")
	}

	if err := os.Chmod(tool, 0o644); err != nil {
		t.Fatalf("chmod -x: %v", err)
	}
	commitAll(t, e.member, "make tool non-executable again")
	fp3, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp3 != fp1 {
		t.Errorf("fingerprint after exec-bit roundtrip = %s, want original %s", fp3, fp1)
	}
}

// TestSourceFingerprintRefusesUntracked proves the baseline fingerprint
// refuses any uncommitted state (ADR 0024: dirty trees are rejected, never
// folded in): an untracked non-ignored file refuses with a typed
// DirtyWorktreeError naming it, and removing it restores the exact baseline
// digest.
func TestSourceFingerprintRefusesUntracked(t *testing.T) {
	e := newEnv(t)
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	untracked := filepath.Join(e.member, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}
	_, err = e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("SourceFingerprint error = %v, want *DirtyWorktreeError", err)
	}
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Error("errors.Is(err, ErrDirtyWorktree) = false, want true")
	}
	if len(de.Files) != 1 || de.Files[0] != "untracked.txt" {
		t.Errorf("dirty files = %v, want [untracked.txt]", de.Files)
	}

	if err := os.Remove(untracked); err != nil {
		t.Fatalf("gitx: remove: %v", err)
	}
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint after remove: %v", err)
	}
	if fp2 != fp1 {
		t.Errorf("fingerprint after untracked roundtrip = %s, want original %s", fp2, fp1)
	}
}

// TestSourceFingerprintRefusesModifiedTracked proves a modified tracked
// file refuses with the dirty paths named, and restoring a clean tree
// yields the original digest unchanged.
func TestSourceFingerprintRefusesModifiedTracked(t *testing.T) {
	e := newEnv(t)
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	seed := filepath.Join(e.member, "seed.txt")
	if err := os.WriteFile(seed, []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}
	_, err = e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("SourceFingerprint error = %v, want *DirtyWorktreeError", err)
	}
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Error("errors.Is(err, ErrDirtyWorktree) = false, want true")
	}
	if len(de.Files) != 1 || de.Files[0] != "seed.txt" {
		t.Errorf("dirty files = %v, want [seed.txt]", de.Files)
	}

	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "checkout", "--", "seed.txt"); err != nil {
		t.Fatalf("gitx: restore seed.txt: %v", err)
	}
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint after restore: %v", err)
	}
	if fp2 != fp1 {
		t.Errorf("fingerprint after dirty roundtrip = %s, want original %s", fp2, fp1)
	}
}

// TestSourceFingerprintLengthFramesFields proves tree records are
// length-framed: a file named "a" containing "x y" and a file named "a x"
// containing "y" join to identical bytes under the old space-separated
// framing ("100644 blob a x y" in both trees) and must digest differently.
func TestSourceFingerprintLengthFramesFields(t *testing.T) {
	one := t.TempDir()
	initMember(t, one)
	commitFile(t, one, "a", "x y", "a: x y")
	two := t.TempDir()
	initMember(t, two)
	commitFile(t, two, "a x", "y", "a x: y")

	svc := &Service{runner: ExecRunner{}}
	fp1, err := svc.SourceFingerprint(context.Background(), one, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	fp2, err := svc.SourceFingerprint(context.Background(), two, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp1 == fp2 {
		t.Error("fingerprints collide for a→\"x y\" and \"a x\"→y, want different digests")
	}
}

func TestSourceFingerprintExcludesIgnored(t *testing.T) {
	e := newEnv(t)
	commitFile(t, e.member, ".gitignore", "ignored.txt\n", "ignore junk")
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	if err := os.WriteFile(filepath.Join(e.member, "ignored.txt"), []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp2 != fp1 {
		t.Errorf("fingerprint changed after ignored file, want unchanged (%s vs %s)", fp1, fp2)
	}
}

func TestSourceFingerprintTracksSymlinkTarget(t *testing.T) {
	e := newEnv(t)
	link := filepath.Join(e.member, "link")
	if err := os.Symlink("target-a", link); err != nil {
		t.Fatalf("gitx: symlink: %v", err)
	}
	commitAll(t, e.member, "add symlink")
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatalf("gitx: remove: %v", err)
	}
	if err := os.Symlink("target-b", link); err != nil {
		t.Fatalf("gitx: symlink: %v", err)
	}
	commitAll(t, e.member, "repoint symlink")
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp2 == fp1 {
		t.Error("fingerprint unchanged after symlink target change, want change")
	}
}

// TestSourceFingerprintSubmoduleIsGitlink proves submodules are recorded as
// gitlinks — the submodule's internal history is never recursed into — and
// that recording a new submodule commit changes the baseline.
func TestSourceFingerprintSubmoduleIsGitlink(t *testing.T) {
	e := newEnv(t)
	sub := t.TempDir()
	initMember(t, sub)
	commitFile(t, sub, "f.txt", "one\n", "one")

	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "-c", "protocol.file.allow=always", "submodule", "add", sub, "sub"); err != nil {
		t.Fatalf("gitx: submodule add: %v", err)
	}
	commitAll(t, e.member, "add submodule")
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	// The submodule's own HEAD (the member's linked clone) moves without
	// the member recording it: the member is dirty at the gitlink and the
	// fingerprint refuses with exactly that path — never the submodule's
	// internal files, which proves it is not recursed.
	commitFile(t, filepath.Join(e.member, "sub"), "f.txt", "two\n", "two")
	_, err = e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	var de *DirtyWorktreeError
	if !errors.As(err, &de) {
		t.Fatalf("SourceFingerprint error = %v, want *DirtyWorktreeError", err)
	}
	if len(de.Files) != 1 || de.Files[0] != "sub" {
		t.Errorf("dirty files = %v, want [sub]", de.Files)
	}

	// The member records the new submodule commit: the gitlink moves.
	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "add", "sub"); err != nil {
		t.Fatalf("gitx: add submodule bump: %v", err)
	}
	commitAll(t, e.member, "bump submodule")
	fp3, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp3 == fp1 {
		t.Error("fingerprint unchanged after gitlink move, want change")
	}
}
