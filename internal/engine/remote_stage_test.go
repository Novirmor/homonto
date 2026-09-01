package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/remote"
)

// TestRemoteStageBeforeMutateLeavesFirstIntactOnSecondFailure exercises F8: all
// declared remotes must be fetched and verified into staging BEFORE any active
// content or the lock is mutated. A later remote's verification failure must
// leave the earlier remote's active content and the lockfile unchanged.
func TestRemoteStageBeforeMutateLeavesFirstIntactOnSecondFailure(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()

	// First apply: only remote "aaa" (v1). It becomes materialized + locked.
	tarA1 := buildSubagentTarGz(t, fixtures, "aaa", "# A v1")
	pinA1 := pinFor(t, tarA1)
	cfgPath := filepath.Join(repo, "homonto.toml")
	cfg1 := "[subagents.aaa]\nsource=\"remote:file://" + tarA1 + "\"\ndigest=\"" + pinA1.String() + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
	if err := os.WriteFile(cfgPath, []byte(cfg1), 0o644); err != nil {
		t.Fatal(err)
	}
	e1, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	sets1, err := e1.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e1.Apply(context.Background(), sets1); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	contentA := filepath.Join(e1.RemoteRoot, "subagents", "aaa.md")
	if got, _ := os.ReadFile(contentA); string(got) != "# A v1" {
		t.Fatalf("setup: A content = %q, want v1", got)
	}

	// Second apply: "aaa" repinned to v2 content PLUS "bbb" with a wrong pin that
	// fails verification. "aaa" < "bbb" so aaa is processed first; under the old
	// prune-then-materialize-in-loop code aaa would be overwritten to v2 before
	// bbb's failure aborted the run, leaving a partial mutation.
	tarA2 := buildSubagentTarGz(t, t.TempDir(), "aaa", "# A v2 changed")
	pinA2 := pinFor(t, tarA2)
	tarB := buildSubagentTarGz(t, t.TempDir(), "bbb", "# B content")
	wrongB := "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	cfg2 := "[subagents.aaa]\nsource=\"remote:file://" + tarA2 + "\"\ndigest=\"" + pinA2.String() + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" +
		"[subagents.bbb]\nsource=\"remote:file://" + tarB + "\"\ndigest=\"" + wrongB + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + remoteModels
	if err := os.WriteFile(cfgPath, []byte(cfg2), 0o644); err != nil {
		t.Fatal(err)
	}
	e2, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	sets2, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.Apply(context.Background(), sets2); err == nil {
		t.Fatal("apply must fail closed when the second remote fails verification")
	}

	// F8: the first remote's active content is unchanged (still v1).
	if got, _ := os.ReadFile(contentA); string(got) != "# A v1" {
		t.Fatalf("partial mutation: A content changed to %q, want v1 unchanged", got)
	}
	// F8: the lockfile is unchanged (still records aaa at v1).
	lock, err := remote.LoadLock(filepath.Join(repo, ".homonto", "remote.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Get("subagent", "aaa")
	if !ok {
		t.Fatal("lock entry for aaa should remain")
	}
	if entry.Digest != pinA1.String() {
		t.Fatalf("lock mutated: aaa digest = %s, want v1 %s", entry.Digest, pinA1.String())
	}
	// bbb must never have been locked.
	if _, ok := lock.Get("subagent", "bbb"); ok {
		t.Fatal("failed remote bbb must not be recorded in the lock")
	}
}
