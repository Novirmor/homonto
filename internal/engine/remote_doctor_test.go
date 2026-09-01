package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/remote"
)

func applyReviewer(t *testing.T, home, repo, tarPath string, pin remote.Digest) *Engine {
	t.Helper()
	cfg := "[subagents.reviewer]\nsource=\"remote:file://" + tarPath + "\"\ndigest=\"" + pin.String() + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
	cfgPath := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return e
}

// TestDoctorReportsMaterializedRemoteDigestMismatch exercises F30: doctor must
// verify each materialized remote digest against the lock and report a mismatch.
func TestDoctorReportsMaterializedRemoteDigestMismatch(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildSubagentTarGz(t, fixtures, "reviewer", "# real content")
	pin := pinFor(t, tarPath)

	e := applyReviewer(t, home, repo, tarPath, pin)

	for _, l := range e.Doctor() {
		if strings.Contains(l, "does not match locked digest") {
			t.Fatalf("healthy workspace must not report a digest mismatch: %q", l)
		}
	}

	// Tamper the materialized active content so its bytes no longer match the pin.
	active := filepath.Join(e.RemoteRoot, "subagents", "reviewer.md")
	if err := os.WriteFile(active, []byte("# tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range e.Doctor() {
		if strings.Contains(l, "reviewer") && strings.Contains(l, "does not match locked digest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor must report the tampered materialized digest as a finding:\n%s", strings.Join(e.Doctor(), "\n"))
	}
}

// TestRevokedRemoteContentDeactivatedOnApplyFailure exercises F30: revoked but
// still-declared content must be deactivated (removed), not left linked, after a
// revoked apply fails closed.
func TestRevokedRemoteContentDeactivatedOnApplyFailure(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildSubagentTarGz(t, fixtures, "reviewer", "# real content")
	pin := pinFor(t, tarPath)

	applyReviewer(t, home, repo, tarPath, pin)
	active := filepath.Join(repo, ".homonto", "remote", "subagents", "reviewer.md")
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("setup: materialized content should exist: %v", err)
	}

	// Revoke the currently-declared pin, then apply again: it must fail closed AND
	// deactivate the revoked content.
	revoked := filepath.Join(repo, ".homonto", "revoked.json")
	if err := os.WriteFile(revoked, []byte(`["`+pin.String()+`"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(repo, "homonto.toml")
	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err == nil {
		t.Fatal("apply of a revoked pin must fail closed")
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatal("revoked-but-declared content must be deactivated (removed)")
	}
	lock, err := remote.LoadLock(filepath.Join(repo, ".homonto", "remote.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Get("subagent", "reviewer"); ok {
		t.Fatal("revoked content's lock entry must be dropped")
	}
}
