package gitx

import (
	"context"
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

func TestSourceFingerprintIncludesUntracked(t *testing.T) {
	e := newEnv(t)
	fp1, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}

	untracked := filepath.Join(e.member, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("gitx: write: %v", err)
	}
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp2 == fp1 {
		t.Error("fingerprint unchanged after untracked file, want change")
	}

	if err := os.Remove(untracked); err != nil {
		t.Fatalf("gitx: remove: %v", err)
	}
	fp3, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp3 != fp1 {
		t.Errorf("fingerprint after untracked roundtrip = %s, want original %s", fp3, fp1)
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
	// the member recording it, so the gitlink is unchanged and the baseline
	// must be unchanged.
	commitFile(t, filepath.Join(e.member, "sub"), "f.txt", "two\n", "two")
	fp2, err := e.svc.SourceFingerprint(context.Background(), e.member, "HEAD")
	if err != nil {
		t.Fatalf("SourceFingerprint: %v", err)
	}
	if fp2 != fp1 {
		t.Errorf("fingerprint changed without a gitlink change (submodule recursed), want unchanged")
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
