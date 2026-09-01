package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/remote"
)

// buildSubagentTarGz writes a tar.gz containing <name>.md and returns its path.
func buildSubagentTarGz(t *testing.T, dir, name, body string) string {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	hdr := &tar.Header{Name: name + ".md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gzbuf bytes.Buffer
	zw := gzip.NewWriter(&gzbuf)
	zw.Write(raw.Bytes())
	zw.Close()
	p := filepath.Join(dir, name+".tar.gz")
	if err := os.WriteFile(p, gzbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pinFor(t *testing.T, tarPath string) remote.Digest {
	t.Helper()
	src, err := remote.ParseRemoteSource("remote:file://" + tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := remote.Fetch(context.Background(), src, remote.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return remote.CanonicalDigest(tree)
}

// remoteModels was a [models.*.*] baseline that the old tier system required.
// Tiers are gone and remote: subagents are projected verbatim (never rendered
// through agentfm), so an empty baseline is all that's needed.
const remoteModels = ""

func TestRemoteSubagentEndToEnd(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildSubagentTarGz(t, fixtures, "reviewer", "# remote reviewer agent")
	pin := pinFor(t, tarPath)

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

	// Remote content materialized at the deterministic remote root.
	contentFile := filepath.Join(e.RemoteRoot, "subagents", "reviewer.md")
	got, err := os.ReadFile(contentFile)
	if err != nil {
		t.Fatalf("remote content not materialized: %v", err)
	}
	if string(got) != "# remote reviewer agent" {
		t.Fatalf("materialized content mismatch: %q", got)
	}

	// Lock records provenance; cache holds the pin.
	lock, err := remote.LoadLock(filepath.Join(repo, ".homonto", "remote.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := lock.Get("subagent", "reviewer"); !ok || entry.Digest != pin.String() {
		t.Fatalf("lock missing/incorrect entry: %+v ok=%v", entry, ok)
	}
	cache := &remote.Cache{Root: filepath.Join(repo, ".homonto", "cache", "remote")}
	if !cache.Has(pin) {
		t.Fatal("verified content should be cached")
	}

	// Second apply is idempotent (no error, content still present).
	sets2, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets2); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	// De-declare → prune: the remote content and lock entry are gone.
	if err := os.WriteFile(cfgPath, []byte(remoteModels), 0o644); err != nil {
		t.Fatal(err)
	}
	e2, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	sets3, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.Apply(context.Background(), sets3); err != nil {
		t.Fatalf("prune apply: %v", err)
	}
	if _, err := os.Stat(contentFile); !os.IsNotExist(err) {
		t.Fatal("de-declared remote content should be pruned")
	}
	lock2, _ := remote.LoadLock(filepath.Join(repo, ".homonto", "remote.lock.json"))
	if _, ok := lock2.Get("subagent", "reviewer"); ok {
		t.Fatal("de-declared lock entry should be dropped")
	}
}

func TestRemoteSubagentPinMismatchAbortsApply(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildSubagentTarGz(t, fixtures, "reviewer", "# real content")

	// Declare a wrong pin: apply must fail closed and write no remote content.
	wrong := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	cfg := "[subagents.reviewer]\nsource=\"remote:file://" + tarPath + "\"\ndigest=\"" + wrong + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
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
	if err := e.Apply(context.Background(), sets); err == nil {
		t.Fatal("apply must fail closed on a pin mismatch")
	}
	if _, err := os.Stat(filepath.Join(e.RemoteRoot, "subagents", "reviewer.md")); !os.IsNotExist(err) {
		t.Fatal("no remote content should be written on a pin mismatch")
	}
}

func TestRemoteSubagentRollbackAndRevocation(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarV1 := buildSubagentTarGz(t, fixtures, "reviewer", "# v1")
	pinV1 := pinFor(t, tarV1)
	tarV2 := buildSubagentTarGz(t, t.TempDir(), "reviewer", "# v2 different content")
	pinV2 := pinFor(t, tarV2)
	if pinV1.Equal(pinV2) {
		t.Fatal("fixtures must differ")
	}
	cfgPath := filepath.Join(repo, "homonto.toml")
	contentFile := filepath.Join(repo, ".homonto", "remote", "subagents", "reviewer.md")
	cache := &remote.Cache{Root: filepath.Join(repo, ".homonto", "cache", "remote")}

	apply := func(tarPath string, pin remote.Digest) {
		t.Helper()
		cfg := "[subagents.reviewer]\nsource=\"remote:file://" + tarPath + "\"\ndigest=\"" + pin.String() + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
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
	}

	// v1 → v2 → roll back to v1.
	apply(tarV1, pinV1)
	apply(tarV2, pinV2)
	if got, _ := os.ReadFile(contentFile); string(got) != "# v2 different content" {
		t.Fatalf("expected v2 content, got %q", got)
	}
	// Rollback: revert the pin. v1 is still cached, so this resolves from cache.
	if !cache.Has(pinV1) {
		t.Fatal("prior pin should remain cached for rollback")
	}
	apply(tarV1, pinV1)
	if got, _ := os.ReadFile(contentFile); string(got) != "# v1" {
		t.Fatalf("rollback failed, content = %q", got)
	}

	// Revocation: ban the current pin; the next apply must fail closed.
	revoked := filepath.Join(repo, ".homonto", "revoked.json")
	if err := os.WriteFile(revoked, []byte(`["`+pinV1.String()+`"]`), 0o644); err != nil {
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
	if err := e.Apply(context.Background(), sets); err == nil {
		t.Fatal("apply of a revoked pin must fail closed")
	}
}

func TestRemoteSubagentGCReclaimsAfterPrune(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildSubagentTarGz(t, fixtures, "reviewer", "# to be pruned")
	pin := pinFor(t, tarPath)
	cfgPath := filepath.Join(repo, "homonto.toml")
	cache := &remote.Cache{Root: filepath.Join(repo, ".homonto", "cache", "remote")}

	cfg := "[subagents.reviewer]\nsource=\"remote:file://" + tarPath + "\"\ndigest=\"" + pin.String() + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	if !cache.Has(pin) {
		t.Fatal("pin should be cached after apply")
	}
	// De-declare → apply should prune content and GC the now-unreferenced cache entry.
	if err := os.WriteFile(cfgPath, []byte(remoteModels), 0o644); err != nil {
		t.Fatal(err)
	}
	e2, _ := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	sets2, _ := e2.Plan()
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatal(err)
	}
	// Apply keeps the cache entry (so a revert can roll back). It is reclaimed
	// only by an explicit GC once no lock entry references it.
	if !cache.Has(pin) {
		t.Fatal("apply must not GC; de-declared content stays cached for rollback")
	}
	dryRun, err := e2.GCRemoteCache(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dryRun) != 1 || !dryRun[0].Equal(pin) {
		t.Fatalf("dry-run GC should report %s, got %+v", pin, dryRun)
	}
	if !cache.Has(pin) {
		t.Fatal("dry-run must not delete")
	}
	if _, err := e2.GCRemoteCache(false); err != nil {
		t.Fatal(err)
	}
	if cache.Has(pin) {
		t.Fatal("explicit GC should reclaim the unreferenced cache entry")
	}
}
