//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package applylock

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireReleasePublishesCompatibleLegacyClaim(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, lockName)
	lk, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	guardianPath, err := GuardianPath(dir, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	guardian, err := os.Lstat(guardianPath)
	if err != nil {
		t.Fatalf("guardian should exist while held: %v", err)
	}
	claim, err := os.Lstat(legacyPath)
	if err != nil {
		t.Fatalf("legacy claim should exist while held: %v", err)
	}
	if !os.SameFile(guardian, claim) {
		t.Fatal("legacy claim is not the guardian hard link")
	}
	if old, err := os.OpenFile(legacyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_ = old.Close()
		t.Fatal("old O_EXCL holder acquired a current lock")
	} else if !os.IsExist(err) {
		t.Fatalf("old O_EXCL holder error = %v, want EEXIST", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, err := os.Lstat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy claim after release = %v, want absent", err)
	}
	if _, err := os.Lstat(guardianPath); err != nil {
		t.Fatalf("guardian should remain reusable after release: %v", err)
	}
	lk2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	if err := lk2.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}

func TestGuardianPathCanonicalizesAliasesIntoOwnedMetadata(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, ".homonto")
	legacyPath := filepath.Join(root, "records", ".lock")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	direct, err := GuardianPath(metadata, legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	throughAlias, err := GuardianPath(filepath.Join(alias, ".homonto"), filepath.Join(alias, "records", ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if direct != throughAlias {
		t.Fatalf("guardian aliases differ: direct=%q alias=%q", direct, throughAlias)
	}
	rel, err := filepath.Rel(metadata, direct)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("guardian %q is not under owned metadata %q", direct, metadata)
	}
}

func TestOldLegacyClaimBlocksNewHolder(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "records", ".lock")
	metadata := filepath.Join(root, ".homonto")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.OpenFile(legacyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.WriteString("old holder\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquirePath(legacyPath, metadata); !errors.Is(err, ErrHeld) {
		t.Fatalf("new holder over old O_EXCL claim = %v, want ErrHeld", err)
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil || string(data) != "old holder\n" {
		t.Fatalf("old O_EXCL claim changed: data=%q err=%v", data, err)
	}
}

func TestAcquireRejectsForeignGuardianClaim(t *testing.T) {
	root := t.TempDir()
	metadata := filepath.Join(root, ".homonto")
	firstPath := filepath.Join(root, "records", ".first.lock")
	secondPath := filepath.Join(root, "records", ".second.lock")
	if err := os.MkdirAll(filepath.Dir(firstPath), 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := AcquirePath(firstPath, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	firstGuardian, err := GuardianPath(metadata, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(firstGuardian, secondPath); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquirePath(secondPath, metadata); !errors.Is(err, ErrHeld) {
		t.Fatalf("new holder accepted a foreign guardian claim: %v", err)
	}
	foreign, err := os.Lstat(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	guardian, err := os.Lstat(firstGuardian)
	if err != nil || !os.SameFile(guardian, foreign) {
		t.Fatalf("foreign guardian claim was changed: guardian=%v claim=%v err=%v", guardian, foreign, err)
	}
}

func TestReleasePreservesReplacedLegacyClaim(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, ".lock")
	metadata := filepath.Join(root, ".homonto")
	lk, err := AcquirePath(legacyPath, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(legacyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("foreign claim\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lk.Release(); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("release after replacement = %v, want preserved replacement error", err)
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil || string(data) != "foreign claim\n" {
		t.Fatalf("replaced claim was not preserved: data=%q err=%v", data, err)
	}
}

func TestAcquirePathSubprocessUsesCanonicalGuardianAcrossTMPDIR(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "records", ".lock")
	metadata := filepath.Join(root, ".homonto")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestAcquirePathSubprocessHolder$")
	cmd.Dir = root
	cmd.Env = append(testEnvWithout("TMPDIR", "HOMONTO_APPLYLOCK_TEST_HOLDER", "HOMONTO_APPLYLOCK_TEST_LEGACY", "HOMONTO_APPLYLOCK_TEST_ROOT"),
		"TMPDIR="+t.TempDir(),
		"HOMONTO_APPLYLOCK_TEST_HOLDER=1",
		"HOMONTO_APPLYLOCK_TEST_LEGACY=alias/records/.lock",
		"HOMONTO_APPLYLOCK_TEST_ROOT=alias/.homonto",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "held\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("holder readiness = %q, %v, stderr=%s", line, err, stderr.String())
	}
	if _, err := AcquirePath(legacyPath, metadata); !errors.Is(err, ErrHeld) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("absolute contender while relative holder runs = %v, want ErrHeld", err)
	}
	if old, err := os.OpenFile(legacyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_ = old.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("old O_EXCL contender acquired a subprocess holder lock")
	} else if !os.IsExist(err) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("old O_EXCL contender error = %v, want EEXIST", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("holder exited successfully after SIGKILL")
	}

	recovered, err := AcquirePath(legacyPath, metadata)
	if err != nil {
		t.Fatalf("new holder did not recover its exact retained claim: %v", err)
	}
	if err := recovered.Release(); err != nil {
		t.Fatalf("release recovered holder: %v", err)
	}
}

func TestAcquirePathSubprocessHolder(t *testing.T) {
	if os.Getenv("HOMONTO_APPLYLOCK_TEST_HOLDER") == "" {
		return
	}
	lk, err := AcquirePath(os.Getenv("HOMONTO_APPLYLOCK_TEST_LEGACY"), os.Getenv("HOMONTO_APPLYLOCK_TEST_ROOT"))
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer lk.Release()
	if _, err := fmt.Fprintln(os.Stdout, "held"); err != nil {
		t.Fatal(err)
	}
	select {}
}

func testEnvWithout(names ...string) []string {
	blocked := make(map[string]bool, len(names))
	for _, name := range names {
		blocked[name] = true
	}
	var out []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			out = append(out, entry)
		}
	}
	return out
}
